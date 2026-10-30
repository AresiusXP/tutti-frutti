package room

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// scriptReader yields its bytes then io.EOF.
func scriptReader(b ...byte) io.Reader { return bytes.NewReader(b) }

func TestRandomCode(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"first symbols", []byte{0, 1, 2, 3}, "BCDF"},
		{"last symbol", []byte{27, 27, 27, 27}, "9999"},
		{"wraps modulo the alphabet", []byte{28, 29, 30, 31}, "BCDF"},
		{"largest unbiased byte", []byte{251, 251, 251, 251}, "9999"},
		{"biased bytes are rejected", []byte{252, 253, 254, 255, 0, 1, 2, 3}, "BCDF"},
		{"rejection in the middle", []byte{0, 255, 1, 252, 2, 3}, "BCDF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := randomCode(scriptReader(tc.in...))
			if err != nil {
				t.Fatalf("randomCode: %v", err)
			}
			if got != tc.want {
				t.Errorf("randomCode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRandomCodeSourceErrors(t *testing.T) {
	if _, err := randomCode(scriptReader(0, 1)); err == nil {
		t.Error("a source that runs dry must be an error")
	}
	wantErr := errors.New("boom")
	if _, err := randomCode(errReader{wantErr}); !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// Every symbol of the alphabet comes up equally often over all byte values
// (no modulo bias).
func TestRandomCodeIsUnbiased(t *testing.T) {
	var all []byte
	for range 9 {
		for b := range 252 {
			all = append(all, byte(b))
		}
	}
	// Add the rejected tail: it must not change a thing.
	for b := 252; b < 256; b++ {
		all = append(all, byte(b))
	}
	counts := map[rune]int{}
	src := bytes.NewReader(all)
	for {
		code, err := randomCode(src)
		if err != nil {
			break
		}
		for _, r := range code {
			counts[r]++
		}
	}
	if len(counts) != len(codeAlphabet) {
		t.Fatalf("saw %d symbols, want %d", len(counts), len(codeAlphabet))
	}
	for r, n := range counts {
		if n != counts['B'] {
			t.Errorf("symbol %q drawn %d times, 'B' %d: biased", r, n, counts['B'])
		}
	}
}

func TestValidCode(t *testing.T) {
	for _, tc := range []struct {
		code string
		want bool
	}{
		{"KT7P", true}, {"2222", true}, {"BCDF", true},
		{"kt7p", false}, {"KT7", false}, {"KT7PX", false}, {"", false},
		{"AEIO", false}, {"0O1I", false}, {"KT 7", false},
	} {
		if got := validCode(tc.code); got != tc.want {
			t.Errorf("validCode(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestCreateGeneratesValidUniqueCodes(t *testing.T) {
	e := newEnv(t)
	seen := map[string]bool{}
	for i := range 200 {
		code := e.create(playerN(i), "Host")
		if !validCode(code) {
			t.Fatalf("invalid code %q", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
	}
	if st := e.hub.Stats(); st.Rooms != 200 {
		t.Errorf("Stats().Rooms = %d, want 200", st.Rooms)
	}
}

func playerN(i int) string { return fmt.Sprintf("player-%d", i) }

func TestCreateRetriesOnCollision(t *testing.T) {
	// BBBB, BBBB again (taken), then CCCC.
	src := scriptReader(0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1)
	e := newEnv(t, func(c *Config) { c.CodeSource = src })
	if got := e.create("a", "A"); got != "BBBB" {
		t.Fatalf("first code = %q, want BBBB", got)
	}
	if got := e.create("b", "B"); got != "CCCC" {
		t.Errorf("second code = %q, want CCCC after the collision", got)
	}
}

func TestCreateGivesUpAfterTooManyCollisions(t *testing.T) {
	zeros := zeroReader{}
	e := newEnv(t, func(c *Config) { c.CodeSource = zeros })
	e.create("a", "A")
	_, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "b", Name: "B", Avatar: testAvatar})
	if !errors.Is(err, ErrTooManyRooms) {
		t.Errorf("err = %v, want ErrTooManyRooms", err)
	}
	if st := e.hub.Stats(); st.Rooms != 1 {
		t.Errorf("Stats().Rooms = %d, want 1", st.Rooms)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestCreateCodeSourceFailure(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CodeSource = scriptReader(1, 2) })
	_, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "a", Name: "A", Avatar: testAvatar})
	if err == nil || errors.Is(err, ErrTooManyRooms) {
		t.Errorf("err = %v, want a code-source error", err)
	}
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
		want error
	}{
		{"empty name", CreateRequest{PlayerID: "p", Name: "", Avatar: testAvatar}, game.ErrInvalidName},
		{"blank name", CreateRequest{PlayerID: "p", Name: "   ", Avatar: testAvatar}, game.ErrInvalidName},
		{"invisible name", CreateRequest{PlayerID: "p", Name: "\u200b\u200d", Avatar: testAvatar}, game.ErrInvalidName},
		{"name too long", CreateRequest{PlayerID: "p", Name: strings.Repeat("a", game.MaxNameLen+1), Avatar: testAvatar}, game.ErrInvalidName},
		{"emoji too long", CreateRequest{PlayerID: "p", Name: "Ana", Avatar: game.Avatar{Emoji: strings.Repeat("e", 33), Color: "#fff"}}, game.ErrInvalidAvatar},
		{"color too long", CreateRequest{PlayerID: "p", Name: "Ana", Avatar: game.Avatar{Emoji: "🍎", Color: strings.Repeat("c", 33)}}, game.ErrInvalidAvatar},
		{"no player id", CreateRequest{PlayerID: "", Name: "Ana", Avatar: testAvatar}, game.ErrUnknownPlayer},
		{"name at the limit", CreateRequest{PlayerID: "p", Name: strings.Repeat("a", game.MaxNameLen), Avatar: testAvatar}, nil},
		{"empty avatar is fine", CreateRequest{PlayerID: "p", Name: "Ana"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			code, err := e.hub.Create(context.Background(), tc.req)
			if tc.want == nil {
				if err != nil || code == "" {
					t.Fatalf("Create = %q, %v; want a room", code, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if st := e.hub.Stats(); st.Rooms != 0 {
				t.Errorf("a refused Create left %d rooms", st.Rooms)
			}
		})
	}
}

func TestCreateCancelledContext(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.hub.Create(ctx, CreateRequest{PlayerID: "p", Name: "Ana"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCreateHostSeat(t *testing.T) {
	e := newEnv(t)
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "host", Name: "  Ana   María ", Avatar: testAvatar})
	if err != nil {
		t.Fatal(err)
	}
	g := query(e, code, func(r *room) game.Game { return *r.g })
	if g.Phase != game.PhaseLobby || g.HostID != "host" || len(g.Players) != 1 {
		t.Fatalf("game = phase %s host %q players %d", g.Phase, g.HostID, len(g.Players))
	}
	p := g.Players[0]
	if p.Name != "Ana María" {
		t.Errorf("name = %q, want it cleaned", p.Name)
	}
	if p.Connected {
		t.Error("the host seat must start disconnected")
	}
	if p.Avatar != testAvatar {
		t.Errorf("avatar = %+v", p.Avatar)
	}
}

func TestCreateLang(t *testing.T) {
	tests := []struct {
		name string
		lang game.Lang
		want game.Lang
	}{
		{"empty means the default", "", game.DefaultSettings().Lang},
		{"es", game.LangES, game.LangES},
		{"en", game.LangEN, game.LangEN},
		{"nl", game.LangNL, game.LangNL},
		{"unknown falls back to the default", "fr", game.DefaultSettings().Lang},
		{"upper case is unknown", "NL", game.DefaultSettings().Lang},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "h", Name: "Host", Avatar: testAvatar, Lang: tc.lang})
			if err != nil {
				t.Fatal(err)
			}
			info, ok := e.hub.Info(code)
			if !ok || info.Lang != tc.want {
				t.Errorf("Info = %+v, %v; want lang %s", info, ok, tc.want)
			}
			s := query(e, code, func(r *room) game.Settings { return r.g.Settings })
			if s.Lang != tc.want {
				t.Errorf("settings lang = %s, want %s", s.Lang, tc.want)
			}
			// The rest of the settings are the defaults, and valid.
			if err := s.Validate(); err != nil {
				t.Errorf("settings invalid: %v", err)
			}
		})
	}
}

func TestMaxRooms(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxRooms = 2 })
	a := e.create("a", "A")
	e.create("b", "B")
	_, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "c", Name: "C", Avatar: testAvatar})
	if !errors.Is(err, ErrTooManyRooms) {
		t.Fatalf("err = %v, want ErrTooManyRooms", err)
	}

	// A closed room frees its place.
	e.advance(defaultUnclaimedTTL + time.Second)
	if st := e.hub.Stats(); st.Rooms != 0 {
		t.Fatalf("rooms = %d after the unclaimed TTL", st.Rooms)
	}
	if _, ok := e.hub.Info(a); ok {
		t.Error("the closed room is still known")
	}
	if _, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "c", Name: "C", Avatar: testAvatar}); err != nil {
		t.Errorf("Create after the rooms closed: %v", err)
	}
}

func TestNewHubDefaults(t *testing.T) {
	h := NewHub(Config{Store: newFaultStore()})
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	c := h.cfg
	if c.JoinTimeout != 10*time.Second {
		t.Errorf("JoinTimeout default = %v, want 10s", c.JoinTimeout)
	}
	if h.heartbeat != 90*time.Minute {
		t.Errorf("heartbeat = %v, want a quarter of the default SnapshotMaxAge (90m)", h.heartbeat)
	}
	if c.IdleTTL != 30*time.Minute || c.UnclaimedTTL != 2*time.Minute || c.SnapshotEvery != 5*time.Second ||
		c.SnapshotMaxAge != 6*time.Hour || c.JanitorEvery != time.Hour || c.MaxRooms != 1000 {
		t.Errorf("defaults = %+v", c)
	}
	if c.Clock == nil || c.Logger == nil || c.CodeSource == nil {
		t.Errorf("clock/logger/code source not defaulted: %+v", c)
	}
	// Negative values are defaults too.
	h2 := NewHub(Config{Store: newFaultStore(), IdleTTL: -1, MaxRooms: -5})
	t.Cleanup(func() { _ = h2.Shutdown(context.Background()) })
	if h2.cfg.IdleTTL != 30*time.Minute || h2.cfg.MaxRooms != 1000 || h2.cfg.JoinTimeout != 10*time.Second {
		t.Errorf("negative values must default: %+v", h2.cfg)
	}
}

func TestNewHubRequiresStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewHub without a store must panic")
		}
	}()
	NewHub(Config{})
}

func TestInfo(t *testing.T) {
	e := newEnv(t)
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "h", Name: "Host", Avatar: testAvatar, Lang: game.LangEN})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := e.hub.Info(code)
	// The instance id has its own tests (safety_test.go): here it just has to be there and be stable.
	want := Info{Code: code, Phase: game.PhaseLobby, Players: 1, MaxPlayers: game.MaxPlayers, Lang: game.LangEN, Joinable: true, Visibility: visibilityPrivate, RoomInstanceID: got.RoomInstanceID}
	if !ok || got != want {
		t.Errorf("Info = %+v, %v; want %+v", got, ok, want)
	}
	if got, ok := e.hub.Info(strings.ToLower(code)); !ok || got != want {
		t.Errorf("Info(lowercase) = %+v, %v", got, ok)
	}
	if got, ok := e.hub.Info(" " + code + " "); !ok || got != want {
		t.Errorf("Info(padded) = %+v, %v", got, ok)
	}
	if _, ok := e.hub.Info("ZZZZ"); ok {
		t.Error("Info of an unknown code = true")
	}
}

func TestInfoJoinable(t *testing.T) {
	t.Run("full lobby", func(t *testing.T) {
		e := newEnv(t)
		code := e.create("p0", "P")
		for i := 1; i < game.MaxPlayers; i++ {
			e.join(code, playerN(i), "P")
		}
		info, _ := e.hub.Info(code)
		if info.Players != game.MaxPlayers || info.Joinable {
			t.Errorf("full lobby: %+v", info)
		}
	})
	t.Run("leaving frees a seat", func(t *testing.T) {
		e := newEnv(t)
		code := e.create("p0", "P")
		var last Session
		for i := 1; i < game.MaxPlayers; i++ {
			_, last = e.join(code, playerN(i), "P")
		}
		e.handle(code, last, game.CmdLeave, nil)
		info, _ := e.hub.Info(code)
		if info.Players != game.MaxPlayers-1 || !info.Joinable {
			t.Errorf("after a leave: %+v", info)
		}
	})
	t.Run("not joinable in the final phase", func(t *testing.T) {
		l := newLobby3(t)
		finishGame(t, l)
		info, _ := l.e.hub.Info(l.code)
		if info.Phase != game.PhaseFinal || info.Joinable || info.Players != 3 {
			t.Errorf("final: %+v", info)
		}
		l.send(0, game.CmdRematch, nil)
		info, _ = l.e.hub.Info(l.code)
		if info.Phase != game.PhaseLobby || !info.Joinable {
			t.Errorf("after the rematch: %+v", info)
		}
	})
	t.Run("players who left do not count", func(t *testing.T) {
		l := newLobby3(t)
		l.startRound(nil)
		l.send(2, game.CmdLeave, nil)
		info, _ := l.e.hub.Info(l.code)
		if info.Players != 2 || info.Phase != game.PhasePlaying {
			t.Errorf("after a mid-game leave: %+v", info)
		}
	})
}

func TestConnectErrors(t *testing.T) {
	e := newEnv(t)
	if _, err := e.hub.Connect("ZZZZ", newFakeClient("p")); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("unknown room: err = %v, want ErrRoomNotFound", err)
	}
	code := e.create("h", "Host")
	hc := newFakeClient("h")
	hs, err := e.hub.Connect(strings.ToLower(code), hc)
	if err != nil {
		t.Fatalf("Connect(lowercase code): %v", err)
	}
	e.handle(code, hs, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Host", Avatar: testAvatar})

	// Connecting to a room that is closing reports it.
	r := e.room(code)
	e.advance(time.Hour) // a player is in: not idle
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("a room with a player in it was closed")
	}
	r.post(func(r *room) { r.closeRoom(protocol.CloseRoomClosed, "test", true) }, nil)
	eventually(t, "the room to close", func() bool { _, ok := e.hub.Info(code); return !ok })
	if _, err := r.connect(newFakeClient("x")); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("connect to a closed room: err = %v, want ErrRoomClosed", err)
	}
}

func TestStats(t *testing.T) {
	e := newEnv(t)
	if st := e.hub.Stats(); st != (Stats{}) {
		t.Fatalf("fresh Stats = %+v", st)
	}
	a, b := e.create("a", "A"), e.create("b", "B")
	_, sa := e.join(a, "a", "A")
	_, sb := e.join(b, "b", "B")
	_, sa2 := e.connect(a, "a2") // attached, not joined: still a connection
	if st := e.hub.Stats(); st != (Stats{Rooms: 2, Connections: 3}) {
		t.Errorf("Stats = %+v", st)
	}
	sa.Disconnect()
	sa.Disconnect() // idempotent
	e.sync(a)
	if st := e.hub.Stats(); st.Connections != 2 {
		t.Errorf("after Disconnect: %+v", st)
	}
	sa2.Disconnect()
	sb.Disconnect()
	e.syncAll()
	if st := e.hub.Stats(); st != (Stats{Rooms: 2}) {
		t.Errorf("after all disconnected: %+v", st)
	}
}

func TestConcurrentHubUse(t *testing.T) {
	e := newEnv(t)
	code := e.create("host", "Host")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				e.hub.Info(code)
				e.hub.Stats()
				c, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: playerN(i), Name: "P", Avatar: testAvatar})
				if err != nil {
					t.Errorf("Create: %v", err)
					return
				}
				cl := newFakeClient(playerN(i))
				s, err := e.hub.Connect(c, cl)
				if err != nil {
					t.Errorf("Connect: %v", err)
					return
				}
				s.Disconnect()
			}
		}()
	}
	wg.Wait()
	if st := e.hub.Stats(); st.Rooms != 1+8*20 {
		t.Errorf("rooms = %d, want %d", st.Rooms, 1+8*20)
	}
}

func TestStartTwiceAndAfterShutdown(t *testing.T) {
	e := newEnv(t)
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hub.Start(context.Background()); err == nil {
		t.Error("second Start must fail")
	}
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hub.Start(context.Background()); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Start after Shutdown: err = %v, want ErrShuttingDown", err)
	}
}

func TestStartLoadAllFailure(t *testing.T) {
	e := newEnv(t)
	e.store.update(func(f *faultStore) { f.failLoadAll = true })
	if n, err := e.hub.Start(context.Background()); err == nil || n != 0 {
		t.Errorf("Start = %d, %v; want an error", n, err)
	}
}

func TestStartRespectsMaxRooms(t *testing.T) {
	e := newEnv(t)
	codes := []string{e.create("a", "A"), e.create("b", "B"), e.create("c", "C")}
	for _, code := range codes {
		e.waitSaved(code)
	}
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	h2 := e.newHubOn(func(c *Config) { c.MaxRooms = 2 })
	n, err := h2.Start(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("Start = %d, %v; want 2 restored", n, err)
	}
	if st := h2.Stats(); st.Rooms != 2 {
		t.Errorf("rooms = %d", st.Rooms)
	}
}
