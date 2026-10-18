package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

const (
	testOrigin  = "https://tuttifrutti.aresius.xyz"
	otherOrigin = "https://evil.example"
	waitLimit   = 5 * time.Second
)

var fixedNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// recordedFrame is one frame delivered to a fake session.
type recordedFrame struct {
	T string
	D string
}

type fakeSession struct {
	client room.Client
	code   string

	mu          sync.Mutex
	frames      []recordedFrame
	onHandle    func(t string, d json.RawMessage)
	disconnects atomic.Int32
}

func (s *fakeSession) Handle(t string, d json.RawMessage) {
	s.mu.Lock()
	s.frames = append(s.frames, recordedFrame{T: t, D: string(d)})
	fn := s.onHandle
	s.mu.Unlock()
	if fn != nil {
		fn(t, d)
	}
}

func (s *fakeSession) Disconnect() { s.disconnects.Add(1) }

func (s *fakeSession) handled() []recordedFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedFrame(nil), s.frames...)
}

type fakeRooms struct {
	mu         sync.Mutex
	known      map[string]bool
	connectErr error
	onConnect  func(code string, c room.Client)
	sessions   []*fakeSession
	connects   []string
	onHandle   func(t string, d json.RawMessage)
}

func (f *fakeRooms) Create(context.Context, room.CreateRequest) (string, error) { return "KT7P", nil }
func (f *fakeRooms) Info(string) (room.Info, bool)                              { return room.Info{}, false }
func (f *fakeRooms) Stats() room.Stats                                          { return room.Stats{} }

func (f *fakeRooms) Connect(code string, c room.Client) (room.Session, error) {
	f.mu.Lock()
	f.connects = append(f.connects, code)
	err, known, hook, onHandle := f.connectErr, f.known[code], f.onConnect, f.onHandle
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, room.ErrRoomNotFound
	}
	s := &fakeSession{client: c, code: code, onHandle: onHandle}
	f.mu.Lock()
	f.sessions = append(f.sessions, s)
	f.mu.Unlock()
	if hook != nil {
		hook(code, c)
	}
	return s, nil
}

func (f *fakeRooms) connectCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.connects...)
}

func (f *fakeRooms) session(t *testing.T, i int) *fakeSession {
	t.Helper()
	eventually(t, "session %d to exist", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.sessions) > i
	}, i)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[i]
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	rooms  *fakeRooms
	guests *auth.Guests
	reg    *prometheus.Registry
	logs   *syncBuffer
	cookie *http.Cookie
	id     string
	h      *Handler
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newEnv starts a WebSocket handler with a frozen clock (so per-connection and
// per-IP rate limits never refill) and an allowed Origin.
func newEnv(t *testing.T, tweak func(*Deps), hooks ...func(*Handler)) *env {
	t.Helper()
	// Registered first so it runs last: every connection goroutine must be
	// gone once the clients and the server are closed.
	t.Cleanup(func() { assertNoConnGoroutines(t) })

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	guests := auth.NewGuests([]byte("test-secret"), false, logger)
	rooms := &fakeRooms{known: map[string]bool{"KT7P": true, "BCDF": true}}
	reg := prometheus.NewRegistry()
	d := Deps{
		Rooms:          rooms,
		Guests:         guests,
		AllowedOrigins: []string{testOrigin},
		Logger:         logger,
		Registerer:     reg,
		Now:            func() time.Time { return fixedNow },
		// The tests pin the small limit; the production default is larger (shared IP).
		UpgradeRate:  60.0 / 60,
		UpgradeBurst: 20,
	}
	if tweak != nil {
		tweak(&d)
	}
	hh := NewHandler(d)
	for _, hook := range hooks {
		hook(hh)
	}
	srv := httptest.NewServer(hh)

	rec := httptest.NewRecorder()
	id := guests.Issue(rec)
	c := rec.Result().Cookies()[0]
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, rooms: rooms, guests: guests, reg: reg, logs: logs, cookie: c, id: id, h: hh}
}

func (e *env) url() string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") }

type dialOpt func(h http.Header)

func noCookie() dialOpt          { return func(h http.Header) { h.Del("Cookie") } }
func noOrigin() dialOpt          { return func(h http.Header) { h.Del("Origin") } }
func origin(o string) dialOpt    { return func(h http.Header) { h.Set("Origin", o) } }
func header(k, v string) dialOpt { return func(h http.Header) { h.Set(k, v) } }
func rawCookie(v string) dialOpt {
	return func(h http.Header) { h.Set("Cookie", protocol.GuestCookie+"="+v) }
}
func forwardedFor(v string) dialOpt { return header("X-Forwarded-For", v) }
func withCookie(c *http.Cookie) dialOpt {
	return func(h http.Header) { h.Set("Cookie", (&http.Cookie{Name: c.Name, Value: c.Value}).String()) }
}

// guest mints another guest identity (cookie and player id) for the env.
func (e *env) guest() (*http.Cookie, string) {
	rec := httptest.NewRecorder()
	id := e.guests.Issue(rec)
	return rec.Result().Cookies()[0], id
}

func (e *env) tryDial(opts ...dialOpt) (*websocket.Conn, *http.Response, error) {
	e.t.Helper()
	h := http.Header{}
	h.Set("Cookie", (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String())
	h.Set("Origin", testOrigin)
	for _, o := range opts {
		o(h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, e.url(), &websocket.DialOptions{HTTPHeader: h})
	if c != nil {
		c.SetReadLimit(1 << 20)
		e.t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

func (e *env) dial(opts ...dialOpt) *websocket.Conn {
	e.t.Helper()
	c, _, err := e.tryDial(opts...)
	if err != nil {
		e.t.Fatalf("dial: %v", err)
	}
	return c
}

// dialJoined connects and joins room KT7P, returning the connection and the
// server-side fake session.
func (e *env) dialJoined(opts ...dialOpt) (*websocket.Conn, *fakeSession) {
	e.t.Helper()
	n := len(e.rooms.connectCalls())
	c := e.dial(opts...)
	send(e.t, c, "join", `{"code":"KT7P","name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}`)
	return c, e.rooms.session(e.t, n)
}

func send(t *testing.T, c *websocket.Conn, typ, d string) {
	t.Helper()
	frame := `{"t":"` + typ + `","v":` + strconv.Itoa(protocol.Version)
	if d != "" {
		frame += `,"d":` + d
	}
	sendRaw(t, c, frame+"}")
}

func sendRaw(t *testing.T, c *websocket.Conn, s string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
		t.Fatalf("write %.60q: %v", s, err)
	}
}

func read(t *testing.T, c *websocket.Conn) protocol.Envelope {
	t.Helper()
	env, err := tryRead(c, waitLimit)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return env
}

func tryRead(c *websocket.Conn, d time.Duration) (protocol.Envelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	if typ != websocket.MessageText {
		return protocol.Envelope{}, io.ErrUnexpectedEOF
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return protocol.Envelope{}, err
	}
	return env, nil
}

func errorBody(t *testing.T, e protocol.Envelope) protocol.ErrorBody {
	t.Helper()
	if e.T != protocol.TypeError {
		t.Fatalf("frame %q (d=%s), want an error frame", e.T, e.D)
	}
	var b protocol.ErrorBody
	if err := json.Unmarshal(e.D, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func expectError(t *testing.T, c *websocket.Conn, code, ref string) {
	t.Helper()
	got := errorBody(t, read(t, c))
	if got.Code != code || got.Ref != ref {
		t.Fatalf("error = %+v, want code %q ref %q", got, code, ref)
	}
}

// expectClose reads until the connection closes and returns the frames that
// arrived before the close frame and the close code and reason.
func expectClose(t *testing.T, c *websocket.Conn) ([]protocol.Envelope, websocket.CloseError) {
	t.Helper()
	var frames []protocol.Envelope
	for {
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		typ, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			var ce websocket.CloseError
			if status := websocket.CloseStatus(err); status != -1 {
				ce.Code = status
				if e, ok := asCloseError(err); ok {
					ce = e
				}
				return frames, ce
			}
			t.Fatalf("expected a close frame, got %v (frames so far: %d)", err, len(frames))
		}
		if typ != websocket.MessageText {
			t.Fatalf("unexpected message type %v", typ)
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, env)
	}
}

func asCloseError(err error) (websocket.CloseError, bool) {
	for err != nil {
		if ce, ok := err.(websocket.CloseError); ok {
			return ce, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return websocket.CloseError{}, false
		}
		err = u.Unwrap()
	}
	return websocket.CloseError{}, false
}

func expectCloseCode(t *testing.T, c *websocket.Conn, want int) []protocol.Envelope {
	t.Helper()
	frames, ce := expectClose(t, c)
	if int(ce.Code) != want {
		t.Fatalf("close code = %d (%q), want %d", ce.Code, ce.Reason, want)
	}
	return frames
}

func eventually(t *testing.T, format string, cond func() bool, args ...any) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for "+format, args...)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// counter returns the value of a labelled counter in reg (0 when absent).
func counter(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	metric:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if len(got) != len(labels) {
				continue
			}
			for k, v := range labels {
				if got[k] != v {
					continue metric
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// labelValues lists every value of one label of a metric family.
func labelValues(t *testing.T, reg *prometheus.Registry, name, label string) []string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == label {
					out = append(out, l.GetValue())
				}
			}
		}
	}
	return out
}

// connGoroutines counts goroutines running the handler or a connection pump.
func connGoroutines() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "internal/ws.(*conn).") || strings.Contains(g, "internal/ws.(*Handler).ServeHTTP") {
			count++
		}
	}
	return count
}

func assertNoConnGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for connGoroutines() > 0 {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Errorf("connection goroutines leaked:\n%s", buf[:n])
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type atomicTime struct {
	mu sync.Mutex
	t  time.Time
}

func (a *atomicTime) Load() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}

func (a *atomicTime) Store(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.t = t
}

func newGuests() *auth.Guests {
	return auth.NewGuests([]byte("unit-secret"), false, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// gauge returns the value of an unlabelled gauge in reg (0 when absent).
func gauge(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name && len(mf.GetMetric()) == 1 {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	return 0
}
