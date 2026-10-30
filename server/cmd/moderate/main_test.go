package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/moderation"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// recorder is a doer that records the last call and answers from a canned value.
type recorder struct {
	path string
	req  any
	resp any
	err  error
}

func (r *recorder) Do(_ context.Context, path string, req, resp any) error {
	r.path, r.req = path, req
	if r.err != nil {
		return r.err
	}
	if r.resp != nil {
		raw, _ := json.Marshal(r.resp)
		return json.Unmarshal(raw, resp)
	}
	return nil
}

func exec(t *testing.T, rec *recorder, env map[string]string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e, func(k string) string { return env[k] }, func(path string) doer {
		if rec.path == "" {
			env["_socket"] = path
		}
		return rec
	})
	return code, o.String(), e.String()
}

func TestSocketPathPrecedence(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"default": {[]string{"status"}, map[string]string{}, moderation.DefaultSocket},
		"env":     {[]string{"status"}, map[string]string{"MODERATION_SOCKET": "/x/env.sock"}, "/x/env.sock"},
		"flag":    {[]string{"--socket", "/x/flag.sock", "status"}, map[string]string{"MODERATION_SOCKET": "/x/env.sock"}, "/x/flag.sock"},
	} {
		rec := &recorder{resp: moderation.StatusResponse{}}
		exec(t, rec, tc.env, tc.args...)
		if got := tc.env["_socket"]; got != tc.want {
			t.Errorf("%s: socket = %q, want %q", name, got, tc.want)
		}
	}
}

func TestCommandsBuildTheRightRequests(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
		want any
	}{
		{[]string{"reports", "list"}, moderation.PathReportsList, moderation.ReportsListRequest{Status: "open", Limit: 20}},
		{[]string{"reports", "list", "--status", "all", "--limit", "5"}, moderation.PathReportsList, moderation.ReportsListRequest{Status: "all", Limit: 5}},
		{[]string{"reports", "show", "abc"}, moderation.PathReportsShow, moderation.ReportRef{ID: "abc"}},
		{[]string{"reports", "resolve", "--operator", "owner", "--note", "removed it", "abc"}, moderation.PathReportsResolve,
			moderation.ResolveRequest{Operator: "owner", ID: "abc", Note: "removed it"}},
		{[]string{"reports", "resolve", "--operator", "owner", "--note", "fine", "--dismiss", "abc"}, moderation.PathReportsResolve,
			moderation.ResolveRequest{Operator: "owner", ID: "abc", Note: "fine", Dismiss: true}},
		{[]string{"restrict", "--operator", "owner", "--guest", "g1", "--reason", "spam", "--days", "7"}, moderation.PathRestrict,
			moderation.RestrictRequest{Operator: "owner", GuestID: "g1", Reason: "spam", Days: 7}},
		{[]string{"restrict", "--operator", "owner", "--report", "r1", "--user", "u1", "--reason", "x"}, moderation.PathRestrict,
			moderation.RestrictRequest{Operator: "owner", ReportID: "r1", UserID: "u1", Reason: "x"}},
		{[]string{"unrestrict", "--operator", "owner", "rid"}, moderation.PathUnrestrict, moderation.UnrestrictRequest{Operator: "owner", ID: "rid"}},
		{[]string{"room", "close", "--operator", "owner", "KT7P"}, moderation.PathRoomClose, moderation.RoomCloseRequest{Operator: "owner", Code: "KT7P"}},
		{[]string{"room", "close", "--operator", "owner", "--instance", "iid"}, moderation.PathRoomClose, moderation.RoomCloseRequest{Operator: "owner", InstanceID: "iid"}},
		{[]string{"room", "close", "--operator", "owner", "--instance", "iid", "KT7P"}, moderation.PathRoomClose, moderation.RoomCloseRequest{Operator: "owner", Code: "KT7P", InstanceID: "iid"}},
		{[]string{"profile", "remove", "--operator", "owner", "--user", "u1"}, moderation.PathProfileRemove, moderation.ProfileRemoveRequest{Operator: "owner", UserID: "u1"}},
		{[]string{"judge", "disable", "--operator", "owner"}, moderation.PathJudge, moderation.JudgeRequest{Operator: "owner", Action: "disable"}},
		{[]string{"judge", "status"}, moderation.PathJudge, moderation.JudgeRequest{Action: "status"}},
		{[]string{"public", "disable", "--operator", "owner"}, moderation.PathPublic, moderation.PublicRequest{Operator: "owner", Action: "disable"}},
		{[]string{"public", "enable", "--operator", "owner"}, moderation.PathPublic, moderation.PublicRequest{Operator: "owner", Action: "enable"}},
		{[]string{"public", "status"}, moderation.PathPublic, moderation.PublicRequest{Action: "status"}},
	} {
		rec := &recorder{}
		code, _, errOut := exec(t, rec, map[string]string{}, tc.args...)
		if code != 0 || rec.path != tc.path || rec.req != tc.want {
			t.Errorf("%v: exit %d (%s), path %q, request %+v; want %q %+v", tc.args, code, errOut, rec.path, rec.req, tc.path, tc.want)
		}
	}
}

func TestUsageErrorsNeverReachTheServer(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"frobnicate"},
		{"reports"},
		{"reports", "frobnicate"},
		{"reports", "show"},
		{"reports", "show", "a", "b"},
		{"reports", "resolve", "abc", "--operator", "owner", "--note", "late flags"}, // flags go before the arguments
		{"unrestrict", "rid", "--operator", "owner"},
		{"restrict", "--nope"},
		{"room", "close", "--operator", "owner"},
		{"room", "close", "--operator", "owner", "A", "B"},
		{"judge", "explode"},
		{"judge"},
		{"public"},
		{"public", "off"},
		{"public", "disable", "extra"},
		{"public", "status", "--operator"},
		{"status", "extra"},
		{"--bogus", "status"},
	} {
		rec := &recorder{}
		code, _, errOut := exec(t, rec, map[string]string{}, args...)
		if code != 2 || rec.path != "" || !strings.Contains(errOut, "usage:") {
			t.Errorf("%v: exit %d, path %q, stderr %q; want a usage error and no request", args, code, rec.path, errOut)
		}
	}
}

func TestFlagsAfterAnArgumentSayWhy(t *testing.T) {
	_, _, errOut := exec(t, &recorder{}, map[string]string{}, "reports", "resolve", "abc", "--note", "x")
	if !strings.Contains(errOut, "flags go before the arguments") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestServerRefusalsAreExitCodeOne(t *testing.T) {
	rec := &recorder{err: &moderation.APIError{Status: 400, Code: moderation.CodeOperatorRequired, Message: "--operator NAME is required"}}
	code, out, errOut := exec(t, rec, map[string]string{}, "judge", "disable")
	if code != 1 || out != "" || !strings.Contains(errOut, "operator_required") || !strings.Contains(errOut, "--operator") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	rec.err = errors.New("cannot reach the server's control socket: connect: no such file")
	if code, _, errOut := exec(t, rec, map[string]string{}, "status"); code != 1 || !strings.Contains(errOut, "control socket") {
		t.Errorf("unreachable: exit %d, %q", code, errOut)
	}
}

func TestShowMarksWhatIsUnverifiedAndNeverPrintsControlCharacters(t *testing.T) {
	evil := "\x1b[2J\x1b]0;pwned\x07 \u202egpj.exe \u200b ok"
	ev, _ := json.Marshal(safety.EvidenceData{
		Source: "client", ObservedText: evil, Claimed: &safety.Ref{PlayerID: "p1", Label: evil},
	})
	view := moderation.ReportView{
		ID: "r1", Status: "open", Kind: "custom_label", Reason: "hate", CreatedAt: time.Now().Add(-3 * time.Hour),
		PurgeAt: time.Now().Add(24 * time.Hour), EvidenceVerified: false, ContentKey: "label:0:abcd", ReporterGuest: "rg",
		Comment: evil, Evidence: ev,
	}
	rec := &recorder{resp: view}
	code, out, _ := exec(t, rec, map[string]string{}, "reports", "show", "r1")
	if code != 0 {
		t.Fatal(code)
	}
	for _, bad := range []string{"\x1b", "\x07", "\u202e", "\u200b"} {
		if strings.Contains(out, bad) {
			t.Errorf("the output contains the raw character %q: a player could drive the operator's terminal\n%s", bad, out)
		}
	}
	for _, want := range []string{"UNVERIFIED", "target:          none known", `\x1b`, "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "UNVERIFIED") < 4 {
		t.Errorf("the evidence, the comment, the claim and the observed text must each be marked:\n%s", out)
	}

	verified := moderation.ReportView{ID: "r2", Status: "open", Kind: "answer", Reason: "spam", EvidenceVerified: true, TargetGuest: "tg", TargetUser: "tu",
		Evidence: json.RawMessage(`{"source":"server","text":"tomate","phase":"review","round":2}`)}
	rec = &recorder{resp: verified}
	_, out, _ = exec(t, rec, map[string]string{}, "reports", "show", "r2")
	if !strings.Contains(out, "VERIFIED") || strings.Contains(out, "UNVERIFIED: the room was gone") || !strings.Contains(out, "target:          guest tg (account tu)") || !strings.Contains(out, `"tomate"`) {
		t.Errorf("verified report:\n%s", out)
	}
}

func TestListShowsTypedTextQuotedAndMarked(t *testing.T) {
	rec := &recorder{resp: moderation.ReportsListResponse{Reports: []moderation.ReportSummary{
		{ID: "r1", Status: "open", Kind: "player", Reason: "spam", CreatedAt: time.Now().Add(-time.Hour), EvidenceVerified: true, HasTarget: true, Comment: "hi\x1b[31m"},
	}}}
	_, out, _ := exec(t, rec, map[string]string{}, "reports", "list")
	if strings.Contains(out, "\x1b") || !strings.Contains(out, "unverified") || !strings.Contains(out, "VERIFIED") {
		t.Errorf("list output:\n%s", out)
	}
	rec = &recorder{resp: moderation.ReportsListResponse{}}
	if _, out, _ := exec(t, rec, map[string]string{}, "reports", "list", "--status", "resolved"); !strings.Contains(out, "no resolved reports") {
		t.Errorf("empty list: %q", out)
	}
}

func TestStatusOutputAndJSON(t *testing.T) {
	st := moderation.StatusResponse{Version: "1.2.3", OpenReports: 2, OldestOpenAgeSeconds: 7200, OldestOpenAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
		ActiveRestrictions: 1, Blocks: 4, Judge: moderation.JudgeStatus{Configured: true}, Public: moderation.PublicStatus{Configured: true}}
	_, out, _ := exec(t, &recorder{resp: st}, map[string]string{}, "status")
	for _, want := range []string{"open reports:         2", "2h0m0s", "active restrictions:  1", "AI judge:             OFF", "public games:         OFF"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output lacks %q:\n%s", want, out)
		}
	}
	st.Public.Enabled = true
	if _, out, _ = exec(t, &recorder{resp: st}, map[string]string{}, "status"); !strings.Contains(out, "public games:         on") {
		t.Errorf("status output with public games on:\n%s", out)
	}
	_, out, _ = exec(t, &recorder{resp: st}, map[string]string{}, "--json", "status")
	var back moderation.StatusResponse
	if err := json.Unmarshal([]byte(out), &back); err != nil || back.OpenReports != 2 || !back.Public.Enabled {
		t.Errorf("--json output = %q (%v)", out, err)
	}
}

func TestPublicSwitchOutput(t *testing.T) {
	rec := &recorder{resp: moderation.PublicResponse{Public: moderation.PublicStatus{Configured: true}}}
	code, out, _ := exec(t, rec, map[string]string{}, "public", "disable", "--operator", "owner")
	if code != 0 || !strings.Contains(out, "public games are OFF") || !strings.Contains(out, "private rooms are unaffected") {
		t.Errorf("disable: exit %d, %q", code, out)
	}
	rec.resp = moderation.PublicResponse{Public: moderation.PublicStatus{Configured: true, Enabled: true}, Warning: "audit failed"}
	code, out, _ = exec(t, rec, map[string]string{}, "public", "enable", "--operator", "owner")
	if code != 0 || !strings.Contains(out, "public games are on") || !strings.Contains(out, "WARNING: audit failed") {
		t.Errorf("enable: exit %d, %q", code, out)
	}
	rec.err = &moderation.APIError{Status: 409, Code: moderation.CodePublicNotConfigured}
	if code, _, errOut := exec(t, rec, map[string]string{}, "public", "status"); code != 1 || !strings.Contains(errOut, "public_not_configured") {
		t.Errorf("refused: exit %d, %q", code, errOut)
	}
}

// ---- end to end over the real socket ----

func TestEndToEndOverTheControlSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "m.sock")
	l, err := moderation.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	mem := store.NewMemory()
	policy := safety.NewPolicy(nil)
	hub := room.NewHub(room.Config{Store: store.NewMemory()})
	t.Cleanup(func() { _ = hub.Shutdown(context.Background()) })
	srv := moderation.NewServer(moderation.Deps{
		Reports: mem, Mod: mem, Policy: policy, Rooms: noRooms{}, Public: hub, Log: slog.New(slog.DiscardHandler), Version: "e2e",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); <-done })

	runCLI := func(args ...string) (int, string, string) {
		var o, e bytes.Buffer
		code := run(append([]string{"--socket", path}, args...), &o, &e, func(string) string { return "" },
			func(p string) doer { return moderation.NewClient(p) })
		return code, o.String(), e.String()
	}
	if code, out, errOut := runCLI("status"); code != 0 || !strings.Contains(out, "e2e") {
		t.Fatalf("status: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCLI("restrict", "--guest", "g1", "--reason", "x"); code != 1 || !strings.Contains(errOut, "operator_required") {
		t.Fatalf("restrict without an operator: %d %q", code, errOut)
	}
	code, out, errOut := runCLI("restrict", "--operator", "owner", "--guest", "g1", "--reason", "spamming", "--days", "3")
	if code != 0 || !policy.Restricted("g1") || !strings.Contains(out, "restricted until") {
		t.Fatalf("restrict: %d %q %q", code, out, errOut)
	}
	acts, _ := mem.ListActions(ctx, 10)
	if len(acts) != 1 || acts[0].Operator != "owner" {
		t.Errorf("audit = %+v", acts)
	}
	if code, _, errOut := runCLI("room", "close", "--operator", "owner", "ZZZZ"); code != 1 || !strings.Contains(errOut, "not_found") {
		t.Errorf("room close of an unknown room: %d %q", code, errOut)
	}

	// The holiday switch: off, audited, shown by status, and on again.
	if code, _, errOut := runCLI("public", "disable"); code != 1 || !strings.Contains(errOut, "operator_required") || !hub.PublicEnabled() {
		t.Fatalf("public disable without an operator: %d %q", code, errOut)
	}
	if code, out, errOut := runCLI("public", "disable", "--operator", "owner"); code != 0 || hub.PublicEnabled() || !strings.Contains(out, "OFF") {
		t.Fatalf("public disable: %d %q %q", code, out, errOut)
	}
	if code, out, _ := runCLI("status"); code != 0 || !strings.Contains(out, "public games:         OFF") {
		t.Errorf("status while off: %d %q", code, out)
	}
	if code, out, errOut := runCLI("public", "enable", "--operator", "owner"); code != 0 || !hub.PublicEnabled() || !strings.Contains(out, "public games are on") {
		t.Fatalf("public enable: %d %q %q", code, out, errOut)
	}
	if acts, _ := mem.ListActions(ctx, 10); len(acts) != 3 {
		t.Errorf("audit = %+v", acts)
	}
}

type noRooms struct{}

func (noRooms) Sweep(context.Context) (map[string][]string, error) { return nil, nil }
func (noRooms) CloseRoom(context.Context, room.CloseTarget) (room.CloseResult, error) {
	return room.CloseResult{}, room.ErrRoomNotFound
}
