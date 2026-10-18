package room

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

func TestMetrics(t *testing.T) {
	l := newLobby3(t)
	reg := l.e.reg

	if got := metricValue(t, reg, "tuttifrutti_rooms"); got != 1 {
		t.Errorf("rooms gauge = %v, want 1", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_connections"); got != 3 {
		t.Errorf("connections gauge = %v, want 3", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_commands_total", "type", "join", "result", "ok"); got != 3 {
		t.Errorf("join ok = %v, want 3", got)
	}

	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	l.send(1, game.CmdStartGame, nil) // not the host
	l.send(0, game.CmdStartGame, nil)
	l.send(0, game.CmdStopLetter, nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.send(0, game.CmdBasta, nil) // already in grace

	for _, tc := range []struct {
		typ, result string
		want        float64
	}{
		{"update_settings", "ok", 1},
		{"start_game", "ok", 1},
		{"start_game", "not_host", 1},
		{"stop_letter", "ok", 1},
		{"set_answer", "ok", 9},
		{"basta", "ok", 1},
		{"basta", "wrong_phase", 1},
	} {
		if got := metricValue(t, reg, "tuttifrutti_commands_total", "type", tc.typ, "result", tc.result); got != tc.want {
			t.Errorf("commands_total{%s,%s} = %v, want %v", tc.typ, tc.result, got, tc.want)
		}
	}
	if got := metricValue(t, reg, "tuttifrutti_rounds_total"); got != 1 {
		t.Errorf("rounds_total = %v, want 1", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_basta_total"); got != 1 {
		t.Errorf("basta_total = %v, want 1", got)
	}

	l.s[2].Disconnect()
	l.e.sync(l.code)
	if got := metricValue(t, reg, "tuttifrutti_connections"); got != 2 {
		t.Errorf("connections gauge after a disconnect = %v, want 2", got)
	}

	if err := l.e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, reg, "tuttifrutti_rooms"); got != 0 {
		t.Errorf("rooms gauge after Shutdown = %v", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_connections"); got != 0 {
		t.Errorf("connections gauge after Shutdown = %v", got)
	}
}

func TestMetricLabelsStayBounded(t *testing.T) {
	l := newLobby3(t)
	before := seriesCount(t, l.e.reg, "tuttifrutti_commands_total")

	// A client chooses the frame types: none of them may become a label value.
	_, stranger := l.e.connect(l.code, "stranger")
	for i := range 200 {
		typ := fmt.Sprintf("garbage-%d", i)
		l.s[0].Handle(typ, nil)   // joined: bad_message
		stranger.Handle(typ, nil) // not joined: not_joined
		stranger.Handle(typ+"x", nil)
	}
	l.e.sync(l.code)

	if got := seriesCount(t, l.e.reg, "tuttifrutti_commands_total"); got > before+2 {
		t.Errorf("commands_total grew from %d to %d series: the type label is unbounded", before, got)
	}
	if got := metricValue(t, l.e.reg, "tuttifrutti_commands_total", "type", "unknown", "result", "bad_message"); got != 200 {
		t.Errorf("unknown/bad_message = %v, want 200", got)
	}
	if got := metricValue(t, l.e.reg, "tuttifrutti_commands_total", "type", "unknown", "result", "not_joined"); got != 400 {
		t.Errorf("unknown/not_joined = %v, want 400", got)
	}
}

func TestMetricsOnRegistryReuse(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := newFaultStore()
	h1 := NewHub(Config{Store: st, Registerer: reg})
	h2 := NewHub(Config{Store: st, Registerer: reg}) // must neither panic nor lose the series
	t.Cleanup(func() {
		_ = h1.Shutdown(context.Background())
		_ = h2.Shutdown(context.Background())
	})
	if _, err := h2.Create(context.Background(), CreateRequest{PlayerID: "p", Name: "P", Avatar: testAvatar}); err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, reg, "tuttifrutti_rooms"); got != 1 {
		t.Errorf("rooms gauge = %v: the second hub does not feed the registered series", got)
	}
}

type failingRegisterer struct{ prometheus.Registerer }

func (failingRegisterer) Register(prometheus.Collector) error {
	return errors.New("no registering today")
}

func TestMetricsSurviveARegistererThatFails(t *testing.T) {
	logs := &logBuf{}
	e := newEnv(t, func(c *Config) {
		c.Registerer = failingRegisterer{prometheus.NewRegistry()}
		c.Logger = newTestLogger(logs)
	})
	e.create("p", "P")
	if !strings.Contains(logs.String(), "registering room metrics failed") {
		t.Errorf("no warning about the failed registration: %q", logs.String())
	}
}

func TestMetricsDisabledWithoutRegisterer(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Registerer = nil })
	code := e.create("p", "P")
	_, s := e.join(code, "p", "P")
	e.handle(code, s, game.CmdBasta, nil) // counted into collectors nobody scrapes
	if st := e.hub.Stats(); st != (Stats{Rooms: 1, Connections: 1}) {
		t.Errorf("Stats = %+v", st)
	}
}

// ---- smaller behaviours ----

func TestHandleCopiesThePayload(t *testing.T) {
	l := newLobby3(t)
	r := l.e.room(l.code)
	gate := make(chan struct{})
	entered := make(chan struct{})
	r.post(func(*room) { close(entered); <-gate }, nil)
	<-entered

	buf := mustJSON(t, game.Kick{PlayerID: "p3"})
	l.s[0].Handle(game.CmdKick, buf)
	for i := range buf { // the transport reuses its read buffer
		buf[i] = 'x'
	}
	close(gate)
	l.e.sync(l.code)

	if got := l.c[2].closeCodes(); !slices.Equal(got, []int{protocol.CloseKicked}) {
		t.Errorf("p3 closes = %v: the payload was not copied", got)
	}
}

func TestErrorAndDueTransitionsInOneCommand(t *testing.T) {
	// A command arrives after a deadline whose timer has not been handled yet:
	// the engine applies the transition and then rejects the command. The
	// sender is told about the error, and everybody about the transition.
	l := newLobby3(t)
	l.startRound(func(s *game.Settings) { s.TimeLimitSeconds = 60 })
	query(l.e, l.code, func(r *room) struct{} { r.stopWake(); return struct{}{} })
	l.e.clock.Set(t0.Add(time.Hour))

	l.send(1, game.CmdStopLetter, nil) // wrong phase: the round is long over

	if got, want := kinds(l.c[1].drain()), []string{"error:wrong_phase", "event:time_up", "event:round_locked", "state"}; !slices.Equal(got, want) {
		t.Errorf("sender frames = %v, want %v", got, want)
	}
	for _, i := range []int{0, 2} {
		if got, want := kinds(l.c[i].drain()), []string{"event:time_up", "event:round_locked", "state"}; !slices.Equal(got, want) {
			t.Errorf("p%d frames = %v, want %v", i+1, got, want)
		}
	}
}

func TestConnectWhileStoppingIsRefused(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	r := e.room(code)
	query(e, code, func(r *room) struct{} { r.stopping = true; return struct{}{} })
	if _, err := r.connect(newFakeClient("h")); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("connect to a stopping room: %v, want ErrShuttingDown", err)
	}
	if st := e.hub.Stats(); st.Connections != 0 {
		t.Errorf("connections = %d", st.Connections)
	}
}

func TestBeginShutdownIsOnlyOnce(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	type res struct {
		seq uint64
		ok  bool
	}
	first := query(e, code, func(r *room) res { s, ok := r.beginShutdown(); return res{s, ok} })
	second := query(e, code, func(r *room) res { s, ok := r.beginShutdown(); return res{s, ok} })
	if !first.ok || second.ok {
		t.Errorf("beginShutdown = %+v then %+v; want it to work once", first, second)
	}
}

func TestUnencodableFrameIsDropped(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	got := query(e, code, func(r *room) []byte { return r.frame("x", make(chan int)) })
	if got != nil {
		t.Errorf("frame = %s, want nil for a payload that cannot be encoded", got)
	}
	if !strings.Contains(e.logs.String(), "encoding a frame failed") {
		t.Error("the failure was not logged")
	}
}

func TestSaverUnit(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	e.waitSaved(code)
	r := e.room(code)
	s := r.saver

	// flush returns immediately for what is already persisted, and honours
	// its context for what is not.
	s.mu.Lock()
	seq := s.seq
	s.mu.Unlock()
	if err := s.flush(context.Background(), seq); err != nil {
		t.Errorf("flush of a persisted snapshot: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.flush(ctx, seq+100); !errors.Is(err, context.Canceled) {
		t.Errorf("flush with a cancelled context: %v", err)
	}

	// After finish: submit is a no-op, finish is idempotent, flush reports the
	// saver is gone.
	s.finish(false)
	s.finish(true)
	if got := s.submit([]byte(`{}`)); got != seq {
		t.Errorf("submit after finish returned seq %d, want %d", got, seq)
	}
	<-s.stopped
	if err := s.flush(context.Background(), seq+1); !errors.Is(err, errSaverStopped) {
		t.Errorf("flush on a stopped saver: %v", err)
	}
}

func TestStressManyRoomsAndClients(t *testing.T) {
	const (
		rooms   = 20
		players = 5
		rounds  = 30
	)
	e := newEnv(t, func(c *Config) { c.MaxRooms = rooms * 2 })

	type account struct {
		mu      sync.Mutex
		clients []*fakeClient
	}
	accounts := make([][]*account, rooms)
	var wg sync.WaitGroup
	for ri := range rooms {
		code := e.create(fmt.Sprintf("r%d-p0", ri), "Host")
		accounts[ri] = make([]*account, players)
		for pi := range players {
			accounts[ri][pi] = &account{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				acc := accounts[ri][pi]
				id := fmt.Sprintf("r%d-p%d", ri, pi)
				connect := func() Session {
					c := newFakeClient(id)
					s, err := e.hub.Connect(code, c)
					if err != nil {
						t.Errorf("Connect: %v", err)
						return nil
					}
					acc.mu.Lock()
					acc.clients = append(acc.clients, c)
					acc.mu.Unlock()
					s.Handle(game.CmdJoin, mustJSON(t, protocol.JoinRequest{Code: code, Name: "P", Avatar: testAvatar}))
					return s
				}
				s := connect()
				if s == nil {
					return
				}
				for i := range rounds {
					switch {
					case pi == 0 && i == 3:
						s.Handle(game.CmdUpdateSettings, mustJSON(t, threeCats(nil)))
					case pi == 0 && i == 6:
						s.Handle(game.CmdStartGame, nil)
					case pi == 0 && i == 9:
						s.Handle(game.CmdStopLetter, nil)
					case i%7 == pi: // flap the connection
						s.Disconnect()
						if s = connect(); s == nil {
							return
						}
					default:
						s.Handle(game.CmdSetAnswer, mustJSON(t, game.SetAnswer{Category: i % 3, Text: fmt.Sprintf("secret-%s-%d", id, i)}))
					}
					e.hub.Info(code)
					e.hub.Stats()
				}
			}()
		}
	}
	// Time moves while they play.
	for range 5 {
		e.clock.Advance(3 * time.Second)
	}
	wg.Wait()
	e.syncAll()

	for ri := range rooms {
		for pi := range players {
			id := fmt.Sprintf("r%d-p%d", ri, pi)
			for _, c := range accounts[ri][pi].clients {
				for _, f := range c.drain() {
					if f.V != protocol.Version {
						t.Fatalf("%s: frame version %d", id, f.V)
					}
					if f.T != protocol.TypeState {
						continue
					}
					st := f.state(t)
					if st.View.YouID != id {
						t.Fatalf("%s received the view of %s", id, st.View.YouID)
					}
					if st.View.Phase != game.PhasePlaying && st.View.Phase != game.PhaseGrace {
						continue
					}
					for other := range players {
						token := fmt.Sprintf("secret-r%d-p%d-", ri, other)
						if other != pi && strings.Contains(string(f.Raw), token) {
							t.Fatalf("%s saw an answer of p%d while playing: %s", id, other, f.Raw)
						}
					}
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if st := e.hub.Stats(); st != (Stats{}) {
		t.Errorf("Stats after Shutdown = %+v", st)
	}
	// Every room was saved.
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil || len(snaps) != rooms {
		t.Errorf("stored snapshots = %d, %v; want %d", len(snaps), err, rooms)
	}
}
