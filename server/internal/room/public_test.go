package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

func createPublic(e *env, id string, lang game.Lang) string {
	e.t.Helper()
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: id, Name: id, Avatar: testAvatar, Lang: lang, Visibility: visibilityPublic})
	if err != nil {
		e.t.Fatal(err)
	}
	e.sync(code)
	return code
}

func TestVisibilityContract(t *testing.T) {
	for _, visibility := range []string{"", "private", "public", "PUBLIC", "unknown", " public "} {
		t.Run(visibility, func(t *testing.T) {
			e := newEnv(t)
			code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "host", Name: "Host", Visibility: visibility})
			valid := visibility == "" || visibility == "private" || visibility == "public"
			if !valid {
				if !errors.Is(err, game.ErrInvalidSettings) {
					t.Fatalf("visibility error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := visibility
			if want == "" {
				want = "private"
			}
			info, ok := e.hub.Info(code)
			if !ok || info.Visibility != want {
				t.Fatalf("Info = %+v, %v", info, ok)
			}
			c, _ := e.join(code, "host", "Host")
			st := lastState(t, c.drain())
			limit := 0
			if want == "public" {
				limit = 90
			}
			if st.Visibility != want || st.View.Settings.TimeLimitSeconds != limit {
				t.Fatalf("state visibility %q, limit %d", st.Visibility, st.View.Settings.TimeLimitSeconds)
			}
			e.waitSaved(code)
			p, _ := e.stored(code)
			if p.Visibility != want || p.Version != 1 {
				t.Fatalf("persisted visibility/version: %+v", p)
			}
		})
	}
}

func TestPublicDirectoryEligibilityAndCopies(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CodeSource = codeSource("BBBB", "CCCC", "DDDD", "FFFF") })
	private := e.create("private", "Private")
	e.join(private, "private", "Private")
	unclaimed := createPublic(e, "waiting", game.LangES)
	_, unjoined := e.connect(unclaimed, "waiting")
	_ = unjoined
	code := createPublic(e, "host", game.LangNL)
	_, host := e.join(code, "host", "Host")
	e.join(code, "guest", "Guest")
	newer := createPublic(e, "new", game.LangEN)
	e.join(newer, "new", "New")
	rooms := e.hub.PublicRooms("")
	if len(rooms) != 2 || rooms[0].Code != code || rooms[1].Code != newer {
		t.Fatalf("directory = %+v", rooms)
	}
	if rooms[0].Lang != game.LangNL || rooms[0].Players != 2 || len(rooms[0].CategoryLabels) != 6 || rooms[0].TimeLimitSeconds != 90 {
		t.Fatalf("summary = %+v", rooms[0])
	}
	rooms[0].CategoryLabels[0] = "mutated"
	rooms[0].Code = "oops"
	filtered := e.hub.PublicRooms(game.LangNL)
	if len(filtered) != 1 || filtered[0].Code != code || filtered[0].CategoryLabels[0] == "mutated" {
		t.Fatalf("directory was mutated: %+v", filtered)
	}
	if got := e.hub.PublicRooms(game.LangES); len(got) != 0 {
		t.Fatalf("unclaimed lobby listed: %+v", got)
	}
	host.Disconnect()
	e.sync(code)
	if got := e.hub.PublicRooms(game.LangNL); len(got) != 0 {
		t.Fatal("offline host listed")
	}
	_, host = e.join(code, "host", "Host")
	e.handle(code, host, game.CmdStartGame, nil)
	if got := e.hub.PublicRooms(game.LangNL); len(got) != 0 {
		t.Fatal("started game listed")
	}
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.hub.PublicRooms(""); len(got) != 0 {
		t.Fatal("shutdown directory not empty")
	}
}

func TestPublicDirectoryBoundOrderingAndConcurrency(t *testing.T) {
	e := newEnv(t)
	codes := make([]string, 55)
	for i := range codes {
		id := fmt.Sprintf("h%d", i)
		codes[i] = createPublic(e, id, game.LangEN)
		e.join(codes[i], id, id)
	}
	slices.Sort(codes) // all created at t0: code is the stable tie breaker
	rooms := e.hub.PublicRooms("")
	if len(rooms) != 50 {
		t.Fatalf("directory length = %d", len(rooms))
	}
	for i, room := range rooms {
		if room.Code != codes[i] {
			t.Fatalf("directory position %d: %s, want %s", i, room.Code, codes[i])
		}
	}
	// An actor blocked on unrelated work cannot block directory readers.
	r := e.room(codes[0])
	entered, release := make(chan struct{}), make(chan struct{})
	r.post(func(*room) { close(entered); <-release }, nil)
	<-entered
	done := make(chan []api.PublicRoom, 1)
	go func() { done <- e.hub.PublicRooms("") }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("directory waited for room mailbox")
	}
	close(release)
	syncRoom(r)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				for _, entry := range e.hub.PublicRooms("") {
					entry.CategoryLabels[0] = "caller-owned"
				}
			}
		})
	}
	for i := range 8 {
		e.join(codes[0], fmt.Sprintf("g%d", i), "Guest")
	}
	wg.Wait()
	for _, room := range e.hub.PublicRooms("") {
		if room.CategoryLabels[0] == "caller-owned" {
			t.Fatal("caller mutated published labels")
		}
	}
}

func TestPersistedVisibilityCompatibilityAndValidation(t *testing.T) {
	g := game.NewGame(game.Player{ID: "host", Name: "Host"}, t0)
	raw, err := encodePersisted("BBBB", t0, t0, g)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "visibility")
	raw, _ = json.Marshal(old)
	p, _, err := decodePersisted(store.Snapshot{Code: "BBBB", State: raw})
	if err != nil || p.Visibility != visibilityPrivate {
		t.Fatalf("legacy visibility = %q, %v", p.Visibility, err)
	}
	old["visibility"] = json.RawMessage(`"unknown"`)
	raw, _ = json.Marshal(old)
	if _, _, err := decodePersisted(store.Snapshot{Code: "BBBB", State: raw}); err == nil {
		t.Fatal("unknown visibility accepted")
	}
	if _, err := g.AddAutomated(t0, game.Player{ID: "bot:one", Name: "One"}); err != nil {
		t.Fatal(err)
	}
	g.HostID = "bot:one"
	raw, _ = encodeRoomState(Persisted{Code: "BBBB", Visibility: visibilityPublic}, g)
	if _, _, err := decodePersisted(store.Snapshot{Code: "BBBB", State: raw}); err == nil {
		t.Fatal("automated host snapshot accepted")
	}
}

func TestAutomatedSnapshotsRequirePublicVisibility(t *testing.T) {
	g := game.NewGame(game.Player{ID: "host", Name: "Host"}, t0)
	g.Settings.TimeLimitSeconds = 90
	if _, err := g.AddAutomated(t0, game.Player{ID: "bot:one", Name: "One"}); err != nil {
		t.Fatal(err)
	}
	gameJSON, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []string{"", visibilityPrivate, visibilityPublic} {
		t.Run("visibility="+visibility, func(t *testing.T) {
			// An empty field is omitted, as in a legacy snapshot.
			raw, err := json.Marshal(Persisted{Version: persistedVersion, Code: "BBBB", Visibility: visibility, Game: gameJSON})
			if err != nil {
				t.Fatal(err)
			}
			_, restored, err := decodePersisted(store.Snapshot{Code: "BBBB", State: raw})
			if visibility != visibilityPublic {
				if err == nil {
					t.Fatal("private snapshot restored automated seats")
				}
				return
			}
			if err != nil || restored == nil || !restored.IsAutomated("bot:one") {
				t.Fatalf("supported public snapshot failed to restore: %v", err)
			}
		})
	}
}
