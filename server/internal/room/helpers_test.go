package room

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// t0 is the instant every test clock starts at.
var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

var testAvatar = game.Avatar{Emoji: "🍎", Color: "#e11"}

// TestMain checks that no test leaves a goroutine behind.
func TestMain(m *testing.M) {
	before := runtime.NumGoroutine()
	code := m.Run()
	if code == 0 {
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > before {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			fmt.Fprintf(os.Stderr, "goroutine leak: %d goroutines before the tests, %d after\n%s\n", before, n, buf)
			code = 1
		}
	}
	os.Exit(code)
}

// eventually polls cond (a tiny bounded wait for work done by other
// goroutines, never a substitute for ordering).
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// ---- fake client ----

type closeRec struct {
	code   int
	reason string
}

type frameRec struct {
	T   string
	V   int
	D   json.RawMessage
	Raw []byte
}

func (f frameRec) event(t testing.TB) game.Event {
	t.Helper()
	var ev game.Event
	if err := json.Unmarshal(f.D, &ev); err != nil {
		t.Fatalf("decoding event %s: %v", f.Raw, err)
	}
	return ev
}

func (f frameRec) state(t testing.TB) protocol.State {
	t.Helper()
	var st protocol.State
	if err := json.Unmarshal(f.D, &st); err != nil {
		t.Fatalf("decoding state %s: %v", f.Raw, err)
	}
	return st
}

func (f frameRec) errBody(t testing.TB) protocol.ErrorBody {
	t.Helper()
	var eb protocol.ErrorBody
	if err := json.Unmarshal(f.D, &eb); err != nil {
		t.Fatalf("decoding error %s: %v", f.Raw, err)
	}
	return eb
}

// kind summarises a frame: "state", "event:<type>" or "error:<code>".
func (f frameRec) kind() string {
	switch f.T {
	case protocol.TypeEvent:
		var ev game.Event
		_ = json.Unmarshal(f.D, &ev)
		return "event:" + string(ev.Type)
	case protocol.TypeError:
		var eb protocol.ErrorBody
		_ = json.Unmarshal(f.D, &eb)
		return "error:" + eb.Code
	}
	return f.T
}

func kinds(frames []frameRec) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.kind()
	}
	return out
}

// fakeClient implements Client by recording what the room does to it.
type fakeClient struct {
	id string

	mu        sync.Mutex
	frames    []frameRec
	closes    []closeRec
	refuse    bool // Send reports a full queue
	panicSend bool
}

var _ Client = (*fakeClient)(nil)

func newFakeClient(id string) *fakeClient { return &fakeClient{id: id} }

func (c *fakeClient) PlayerID() string { return c.id }

func (c *fakeClient) Send(frame []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.panicSend {
		panic("fakeClient: Send blew up")
	}
	if c.refuse {
		return false
	}
	rec := frameRec{Raw: bytes.Clone(frame)}
	var env protocol.Envelope
	if err := json.Unmarshal(frame, &env); err == nil {
		rec.T, rec.V, rec.D = env.T, env.V, env.D
	}
	c.frames = append(c.frames, rec)
	return true
}

func (c *fakeClient) Close(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes = append(c.closes, closeRec{code, reason})
}

func (c *fakeClient) setRefuse(v bool) {
	c.mu.Lock()
	c.refuse = v
	c.mu.Unlock()
}

// drain returns and forgets the frames received so far.
func (c *fakeClient) drain() []frameRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.frames
	c.frames = nil
	return out
}

func (c *fakeClient) closeCodes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.closes))
	for i, r := range c.closes {
		out[i] = r.code
	}
	return out
}

func (c *fakeClient) closeRecs() []closeRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]closeRec(nil), c.closes...)
}

// lastState returns the newest state frame in frames.
func lastState(t testing.TB, frames []frameRec) protocol.State {
	t.Helper()
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].T == protocol.TypeState {
			return frames[i].state(t)
		}
	}
	t.Fatalf("no state frame among %v", kinds(frames))
	return protocol.State{}
}

func playerView(t testing.TB, v game.View, id string) game.Player {
	t.Helper()
	for _, p := range v.Players {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("player %q not in view (players %v)", id, v.Players)
	return game.Player{}
}

// ---- fault-injecting store ----

var errStoreDown = errors.New("store down")

type saveRec struct {
	code  string
	state []byte
}

// faultStore wraps a Memory store and can fail, hold or hang its writes.
type faultStore struct {
	*store.Memory

	mu              sync.Mutex
	saves           []saveRec
	deletes         []string
	failSaves       int           // fail the next n Saves
	failDeletes     int           // fail the next n Deletes
	hangDeletes     bool          // Delete blocks until its context ends
	failLoadAll     bool          // LoadAll fails
	failJanitor     bool          // DeleteOlderThan fails
	hang            bool          // Save blocks until its context ends
	gate            chan struct{} // when set, Save blocks until it is closed
	janitorCutoffs  []time.Time
	janitorDeletion int64
}

func newFaultStore() *faultStore { return &faultStore{Memory: store.NewMemory()} }

func (f *faultStore) Save(ctx context.Context, s store.Snapshot) error {
	f.mu.Lock()
	f.saves = append(f.saves, saveRec{s.Code, bytes.Clone(s.State)})
	failing := f.failSaves > 0
	if failing {
		f.failSaves--
	}
	hang, gate := f.hang, f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if failing {
		return errStoreDown
	}
	return f.Memory.Save(ctx, s)
}

func (f *faultStore) Delete(ctx context.Context, code string) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, code)
	failing := f.failDeletes > 0
	if failing {
		f.failDeletes--
	}
	hang := f.hangDeletes
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if failing {
		return errStoreDown
	}
	return f.Memory.Delete(ctx, code)
}

func (f *faultStore) LoadAll(ctx context.Context) ([]store.Snapshot, error) {
	f.mu.Lock()
	failing := f.failLoadAll
	f.mu.Unlock()
	if failing {
		return nil, errStoreDown
	}
	return f.Memory.LoadAll(ctx)
}

func (f *faultStore) DeleteOlderThan(ctx context.Context, t time.Time) (int64, error) {
	f.mu.Lock()
	f.janitorCutoffs = append(f.janitorCutoffs, t)
	failing := f.failJanitor
	f.mu.Unlock()
	if failing {
		return 0, errStoreDown
	}
	n, err := f.Memory.DeleteOlderThan(ctx, t)
	f.mu.Lock()
	f.janitorDeletion += n
	f.mu.Unlock()
	return n, err
}

func (f *faultStore) saveCount(code string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.saves {
		if s.code == code {
			n++
		}
	}
	return n
}

func (f *faultStore) deleteCount(code string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.deletes {
		if c == code {
			n++
		}
	}
	return n
}

// has reports whether a snapshot of code is stored.
func (f *faultStore) has(code string) bool {
	snaps, err := f.Memory.LoadAll(context.Background())
	if err != nil {
		return false
	}
	for _, s := range snaps {
		if s.Code == code {
			return true
		}
	}
	return false
}

func (f *faultStore) update(fn func(*faultStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// codeSource returns a randomness source that yields exactly the given room
// codes, one after the other (then io.EOF).
func codeSource(codes ...string) io.Reader {
	var b []byte
	for _, code := range codes {
		for i := range len(code) {
			b = append(b, byte(strings.IndexByte(codeAlphabet, code[i])))
		}
	}
	return bytes.NewReader(b)
}

// ---- log capture ----

type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// ---- environment ----

// env is a Hub on a ManualClock over a fault-injecting store, with metrics on
// a private registry. Its cleanup shuts the hub down and checks that no
// goroutine is left.
type env struct {
	t     *testing.T
	clock *ManualClock
	store *faultStore
	reg   *prometheus.Registry
	logs  *logBuf
	hub   *Hub
}

func newEnv(t *testing.T, mods ...func(*Config)) *env {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > before {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			t.Errorf("goroutine leak: %d before, %d after\n%s", before, n, buf)
		}
	})

	e := &env{
		t:     t,
		clock: NewManualClock(t0),
		store: newFaultStore(),
		reg:   prometheus.NewRegistry(),
		logs:  &logBuf{},
	}
	cfg := Config{
		Store:      e.store,
		Clock:      e.clock,
		Registerer: e.reg,
		Logger:     slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, mod := range mods {
		mod(&cfg)
	}
	e.hub = NewHub(cfg)
	e.hub.minBackoff = time.Second
	e.hub.maxBackoff = 8 * time.Second
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.hub.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("cleanup Shutdown: %v", err)
		}
	})
	return e
}

// newHubOn builds another hub over the same store and clock, as a restarted
// process would.
func (e *env) newHubOn(mods ...func(*Config)) *Hub {
	cfg := Config{
		Store:  e.store,
		Clock:  e.clock,
		Logger: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, mod := range mods {
		mod(&cfg)
	}
	h := NewHub(cfg)
	h.minBackoff = time.Second
	h.maxBackoff = 8 * time.Second
	e.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	return h
}

func (e *env) create(id, name string) string {
	e.t.Helper()
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: id, Name: name, Avatar: testAvatar})
	if err != nil {
		e.t.Fatalf("Create(%s): %v", id, err)
	}
	e.sync(code) // the room has armed its timers
	return code
}

func (e *env) room(code string) *room {
	e.t.Helper()
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	r := e.hub.rooms[code]
	if r == nil {
		e.t.Fatalf("no room %s", code)
	}
	return r
}

// sync returns once the room has handled everything posted so far.
func (e *env) sync(code string) {
	e.t.Helper()
	syncRoom(e.room(code))
}

// syncRoom returns once r has handled everything posted so far (or is closed).
func syncRoom(r *room) {
	done := make(chan struct{})
	r.post(func(*room) { close(done) }, func() { close(done) })
	<-done
}

func (e *env) syncAll() {
	e.t.Helper()
	e.hub.mu.Lock()
	rooms := make([]*room, 0, len(e.hub.rooms))
	for _, r := range e.hub.rooms {
		rooms = append(rooms, r)
	}
	e.hub.mu.Unlock()
	for _, r := range rooms {
		syncRoom(r)
	}
}

// advance moves the clock by d, stopping at every pending timer on the way so
// that each room handles its wake-up at the exact instant it asked for, and
// waits for the rooms after each step (they re-arm their timers meanwhile).
func (e *env) advance(d time.Duration) {
	e.t.Helper()
	target := e.clock.Now().Add(d)
	for {
		next, ok := e.clock.nextDeadline()
		if !ok || next.After(target) {
			break
		}
		e.clock.Set(next)
		e.syncAll()
	}
	e.clock.Set(target)
	e.syncAll()
}

// nextDeadline is the earliest pending timer.
func (c *ManualClock) nextDeadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var at time.Time
	found := false
	for _, t := range c.timers {
		if !found || t.when.Before(at) {
			at, found = t.when, true
		}
	}
	return at, found
}

// otherTimers counts the pending timers that are not a room's wake-up: the
// savers' backoff sleeps and the janitor's.
func (e *env) otherTimers() int {
	e.t.Helper()
	e.hub.mu.Lock()
	codes := make([]string, 0, len(e.hub.rooms))
	for code := range e.hub.rooms {
		codes = append(codes, code)
	}
	e.hub.mu.Unlock()
	n := e.clock.Pending()
	for _, code := range codes {
		if query(e, code, func(r *room) bool { return r.wake != nil }) {
			n--
		}
	}
	return n
}

// query runs f on the room's goroutine and returns its result.
func query[T any](e *env, code string, f func(*room) T) T {
	e.t.Helper()
	r := e.room(code)
	ch := make(chan T, 1)
	r.post(func(r *room) { ch <- f(r) }, func() {
		var zero T
		ch <- zero
	})
	return <-ch
}

func (e *env) connect(code, id string) (*fakeClient, Session) {
	e.t.Helper()
	c := newFakeClient(id)
	s, err := e.hub.Connect(code, c)
	if err != nil {
		e.t.Fatalf("Connect(%s, %s): %v", code, id, err)
	}
	return c, s
}

// handle sends a frame and waits for the room to process it.
func (e *env) handle(code string, s Session, typ string, payload any) {
	e.t.Helper()
	var d json.RawMessage
	if payload != nil {
		var err error
		if d, err = json.Marshal(payload); err != nil {
			e.t.Fatal(err)
		}
	}
	s.Handle(typ, d)
	e.sync(code)
}

// join connects a client and joins it to the room.
func (e *env) join(code, id, name string) (*fakeClient, Session) {
	e.t.Helper()
	c, s := e.connect(code, id)
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: name, Avatar: testAvatar})
	return c, s
}

// waitSaved waits until the room's newest snapshot reached the store.
func (e *env) waitSaved(code string) {
	e.t.Helper()
	e.sync(code)
	r := e.room(code)
	r.saver.mu.Lock()
	seq := r.saver.seq
	r.saver.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.saver.flush(ctx, seq); err != nil {
		e.t.Fatalf("waiting for the snapshot of %s: %v", code, err)
	}
}

// stored decodes the stored snapshot of a room.
func (e *env) stored(code string) (Persisted, *game.Game) {
	e.t.Helper()
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, s := range snaps {
		if s.Code != code {
			continue
		}
		p, g, err := decodePersisted(s)
		if err != nil {
			e.t.Fatalf("stored snapshot of %s: %v", code, err)
		}
		return p, g
	}
	e.t.Fatalf("no stored snapshot for %s", code)
	return Persisted{}, nil
}

// ---- game helpers ----

// threeCats is a settings update with three categories and one round.
func threeCats(mod func(*game.Settings)) game.UpdateSettings {
	s := game.DefaultSettings()
	s.Rounds = 1
	s.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "pais"}, {ID: "animal"}}
	if mod != nil {
		mod(&s)
	}
	return game.UpdateSettings{Settings: s}
}

// lobby3 returns a room with three joined clients p1 (host), p2 and p3, their
// frames cleared.
type lobby3 struct {
	e    *env
	code string
	c    [3]*fakeClient
	s    [3]Session
}

func newLobby3(t *testing.T, mods ...func(*Config)) *lobby3 {
	t.Helper()
	e := newEnv(t, mods...)
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l.c[i], l.s[i] = e.join(l.code, fmt.Sprintf("p%d", i+1), name)
	}
	l.drain()
	return l
}

func (l *lobby3) drain() {
	for _, c := range l.c {
		c.drain()
	}
}

// send makes player i (0-based) send a frame.
func (l *lobby3) send(i int, typ string, payload any) {
	l.e.t.Helper()
	l.e.handle(l.code, l.s[i], typ, payload)
}

// startRound configures three categories, starts the game and has the chooser
// (p1) stop the alphabet immediately, so the letter is "A".
func (l *lobby3) startRound(mod func(*game.Settings)) {
	l.e.t.Helper()
	l.send(0, game.CmdUpdateSettings, threeCats(mod))
	l.send(0, game.CmdStartGame, nil)
	l.send(0, game.CmdStopLetter, nil)
	st := lastState(l.e.t, l.c[0].drain())
	if st.View.Phase != game.PhasePlaying || st.View.Round.Letter != "A" {
		l.e.t.Fatalf("setup: phase %s letter %q", st.View.Phase, st.View.Round.Letter)
	}
	l.drain()
}

// ---- metrics ----

// metricValue reads a counter or gauge from reg (0 when the series does not
// exist). labels are name=value pairs.
func metricValue(t testing.TB, reg *prometheus.Registry, name string, labels ...string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
	metric:
		for _, m := range mf.GetMetric() {
			for i := 0; i+1 < len(labels); i += 2 {
				found := false
				for _, l := range m.GetLabel() {
					if l.GetName() == labels[i] && l.GetValue() == labels[i+1] {
						found = true
					}
				}
				if !found {
					continue metric
				}
			}
			switch {
			case m.Counter != nil:
				return m.Counter.GetValue()
			case m.Gauge != nil:
				return m.Gauge.GetValue()
			}
		}
	}
	return 0
}

// seriesCount counts the series of a metric family.
func seriesCount(t testing.TB, reg *prometheus.Registry, name string) int {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() == name {
			return len(mf.GetMetric())
		}
	}
	return 0
}

var _ io.Writer = (*logBuf)(nil)
