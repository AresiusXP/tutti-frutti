//go:build unix

package moderation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// shortDir is a short temp directory: a socket path is limited to ~100 bytes
// and t.TempDir() paths on macOS are close to that.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSocketIsPrivateAndRemovedAtShutdown(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "sub", "m.sock") // the directory is created
	oldMask := umaskForTest(0o022)              // a permissive umask must not leak into the socket
	defer umaskForTest(oldMask)

	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want a socket with permissions 0600", fi.Mode())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm()&0o077 != 0 {
		t.Errorf("the directory the server created is %v: it must not be open to others", di.Mode().Perm())
	}

	e := newEnv(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.srv.Serve(ctx, l) }()

	c := NewClient(path)
	var st StatusResponse
	if err := c.Do(ctx, PathStatus, struct{}{}, &st); err != nil || st.Version != "test" {
		t.Fatalf("status over the socket = %+v, %v", st, err)
	}
	err = c.Do(ctx, PathReportsShow, ReportRef{ID: someID}, nil)
	if !IsCode(err, CodeNotFound) {
		t.Errorf("an error over the socket = %v, want not_found", err)
	}
	if err := c.Do(ctx, "/v1/nope", struct{}{}, nil); !IsCode(err, CodeUnknownCommand) {
		t.Errorf("unknown command over the socket = %v", err)
	}
	e.rooms.closeResult = room.CloseResult{Code: "KT7P", RoomInstanceID: instanceID}
	if err := c.Do(ctx, PathRoomClose, RoomCloseRequest{Code: "KT7P"}, nil); !IsCode(err, CodeOperatorRequired) {
		t.Errorf("a mutating command without an operator = %v", err)
	}
	if err := c.Do(ctx, PathRoomClose, RoomCloseRequest{Operator: "owner", Code: "KT7P"}, nil); err != nil {
		t.Errorf("room close over the socket = %v", err)
	}
	huge := RoomCloseRequest{Operator: "owner", Code: strings.Repeat("A", MaxRequestBytes+1)}
	if err := c.Do(ctx, PathRoomClose, huge, nil); !IsCode(err, CodeTooLarge) {
		t.Errorf("an oversized request over the socket = %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("the socket is still there after shutdown: %v", err)
	}
	if err := c.Do(context.Background(), PathStatus, struct{}{}, nil); err == nil || !strings.Contains(err.Error(), "control socket") {
		t.Errorf("a client of a stopped server = %v", err)
	}
}

func TestListenReplacesAStaleSocketButNeverAFile(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "m.sock")

	l1, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	// A crashed process leaves its socket behind (no unlink on close here).
	l1.(interface{ SetUnlinkOnClose(bool) }).SetUnlinkOnClose(false)
	_ = l1.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("setup: the stale socket is gone")
	}
	l2, err := Listen(path)
	if err != nil {
		t.Fatalf("a stale socket must be replaced: %v", err)
	}
	_ = l2.Close()

	file := filepath.Join(dir, "important.txt")
	if err := os.WriteFile(file, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("Listen over a regular file = %v, want a refusal", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "keep me" {
		t.Error("the file was touched")
	}
}

func TestListenInAnUnwritableDirectoryNamesThePathAndTheUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := shortDir(t)
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	cases := map[string]string{
		"existing read-only directory": filepath.Join(locked, "m.sock"),
		"missing directory under it":   filepath.Join(locked, "sub", "m.sock"),
	}
	for name, path := range cases {
		l, err := Listen(path)
		if err == nil {
			_ = l.Close()
			t.Fatalf("%s: Listen succeeded in a directory the server cannot write", name)
		}
		for _, want := range []string{path, fmt.Sprintf("uid %d", os.Geteuid()), "0500"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not mention %q", name, err, want)
			}
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s: error %v does not wrap fs.ErrPermission", name, err)
		}
	}
	if fi, err := os.Stat(locked); err != nil || fi.Mode().Perm() != 0o500 {
		t.Errorf("the directory the server could not use was changed: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(locked, "sub")); !os.IsNotExist(err) {
		t.Errorf("a directory appeared inside the read-only one: %v", err)
	}
}

func TestListenNeverChangesTheModeOfAnExistingDirectory(t *testing.T) {
	dir := shortDir(t)
	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := Listen(filepath.Join(shared, "m.sock"))
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if fi, err := os.Stat(shared); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the existing directory is now %v (%v): Listen must not chmod a directory it did not create", fi.Mode().Perm(), err)
	}
}

func TestListenRefusesBadPaths(t *testing.T) {
	for name, path := range map[string]string{
		"relative": "moderation.sock",
		"too long": "/tmp/" + strings.Repeat("x", 120),
	} {
		if l, err := Listen(path); err == nil {
			_ = l.Close()
			t.Errorf("%s: Listen(%q) succeeded", name, path)
		}
	}
}

func TestRestrictOverTheSocketReachesThePolicy(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "m.sock")
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = e.srv.Serve(ctx, l) }()

	var out RestrictResponse
	err = NewClient(path).Do(ctx, PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "x"}, &out)
	if err != nil || !e.policy.Restricted(targetGuest) {
		t.Fatalf("restrict over the socket = %+v, %v", out, err)
	}
	if acts := e.actions(); len(acts) != 1 || acts[0].Operator != "owner" || acts[0].Action != safety.ActionRestrict {
		t.Errorf("audit = %+v", acts)
	}
}

// The switch of public games, over the socket, against a real room hub.
func TestPublicSwitchOverTheSocketReachesTheHub(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "m.sock")
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	hub := room.NewHub(room.Config{Store: store.NewMemory()})
	t.Cleanup(func() { _ = hub.Shutdown(context.Background()) })
	e := newEnv(t, false)
	e.withDeps(func(d *Deps) { d.Public = hub })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = e.srv.Serve(ctx, l) }()

	create := func(visibility string) error {
		_, err := hub.Create(ctx, room.CreateRequest{PlayerID: "host", Name: "Host", Visibility: visibility})
		return err
	}
	var out PublicResponse
	if err := NewClient(path).Do(ctx, PathPublic, PublicRequest{Operator: "owner", Action: "disable"}, &out); err != nil || out.Public.Enabled {
		t.Fatalf("public disable over the socket = %+v, %v", out, err)
	}
	if hub.PublicEnabled() || !errors.Is(create("public"), room.ErrPublicUnavailable) || create("private") != nil {
		t.Fatal("the hub did not apply the switch")
	}
	if err := NewClient(path).Do(ctx, PathPublic, PublicRequest{Operator: "owner", Action: "enable"}, &out); err != nil || !out.Public.Enabled {
		t.Fatalf("public enable over the socket = %+v, %v", out, err)
	}
	if !hub.PublicEnabled() || create("public") != nil {
		t.Fatal("public rooms are still refused after enable")
	}
	if acts := e.actions(); len(acts) != 2 || acts[0].Action == acts[1].Action ||
		(acts[0].Action != safety.ActionPublicDisable && acts[0].Action != safety.ActionPublicEnable) {
		t.Errorf("audit = %+v", acts)
	}
}
