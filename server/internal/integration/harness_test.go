package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
	"github.com/AresiusXP/tutti-frutti/server/internal/ws"
)

// waitLimit bounds every wait for a frame, a close or a store condition.
const waitLimit = 5 * time.Second

const testSecret = "integration-test-secret-0123456789-abcdef"

// epoch is where every ManualClock starts.
var epoch = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

// ---------------------------------------------------------------------------
// The stack: Hub + store + REST + WebSocket on an httptest.Server.
// ---------------------------------------------------------------------------

type stackOpts struct {
	// store defaults to a fresh in-memory store.
	store store.Store
	// clock defaults to a new ManualClock at start (or epoch).
	clock *room.ManualClock
	start time.Time
	// secret is the guest-cookie secret (default testSecret).
	secret string
	// hub tweaks the room.Config before the Hub is built.
	hub func(*room.Config)
	// wsNow is the ws.Deps.Now clock (pong timestamps, rate limits); nil uses
	// the real clock, so the per-connection token bucket refills on its own.
	wsNow func() time.Time
	// ws tweaks the ws.Deps before the handler is built.
	ws func(*ws.Deps)
	// noForwardedFor makes the stack ignore X-Forwarded-For: every client then
	// shares the TCP peer's address (and its upgrade bucket).
	noForwardedFor bool
	// limits overrides the (very generous) REST rate limits of the harness.
	limits httpapi.Limits
	// idp enables Google login against this fake identity provider.
	idp *authtest.IdP
}

type stack struct {
	t        *testing.T
	clock    *room.ManualClock
	store    store.Store
	hub      *room.Hub
	wsh      *ws.Handler
	guests   *auth.Guests
	sessions *auth.Sessions
	backend  store.Backend // the store, when it also keeps accounts
	idp      *authtest.IdP
	api      *httpapi.Server
	srv      *httptest.Server
	reg      *prometheus.Registry
	logs     *syncBuffer
	origin   string
	restored int
	http     *http.Client

	closeOnce sync.Once
	dialSeq   atomic.Uint32

	mu    sync.Mutex
	codes []string // every room this stack created or restored
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

// newStack wires the real server exactly like cmd/server/main.go does, over an
// httptest.Server whose origin is the only allowed one. Cleanups registered by
// the test (clients) run before the stack is shut down, and the goroutine-leak
// check runs after everything is closed.
func newStack(t *testing.T, o stackOpts) *stack {
	t.Helper()
	registerLeakCheck(t)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	clk := o.clock
	if clk == nil {
		start := o.start
		if start.IsZero() {
			start = epoch
		}
		clk = room.NewManualClock(start)
	}
	st := o.store
	if st == nil {
		st = store.NewMemory()
	}
	secret := o.secret
	if secret == "" {
		secret = testSecret
	}
	reg := prometheus.NewRegistry()

	// Like cmd/server: the store that keeps the snapshots also keeps the
	// accounts and records the statistics.
	backend, _ := st.(store.Backend)
	cfg := room.Config{Store: st, Clock: clk, Logger: logger, Registerer: reg}
	if backend != nil {
		cfg.Recorder = backend
	}
	if o.hub != nil {
		o.hub(&cfg)
	}
	hub := room.NewHub(cfg)
	restored, err := hub.Start(context.Background())
	if err != nil {
		t.Fatalf("hub.Start: %v", err)
	}

	guests := auth.NewGuests([]byte(secret), false, logger)
	sessions := auth.NewSessions([]byte(secret), false, auth.WithSessionClock(clk.Now))
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()
	origins := []string{origin}
	var google *auth.Google
	if o.idp != nil {
		o.idp.SetClock(clk.Now)
		google = auth.NewGoogle(auth.GoogleConfig{
			ClientID: o.idp.ClientID, ClientSecret: o.idp.ClientSecret, RedirectURL: origin + auth.GoogleCallbackPath,
			Secret: []byte(secret), Issuer: o.idp.URL, Now: clk.Now, Logger: logger,
		})
	}

	limits := httpapi.Limits{CreateRate: 1000, CreateBurst: 1 << 20, LookupRate: 1000, LookupBurst: 1 << 20}
	if o.limits.CreateRate > 0 {
		limits.CreateRate = o.limits.CreateRate
	}
	if o.limits.CreateBurst > 0 {
		limits.CreateBurst = o.limits.CreateBurst
	}
	if o.limits.LookupRate > 0 {
		limits.LookupRate = o.limits.LookupRate
	}
	if o.limits.LookupBurst > 0 {
		limits.LookupBurst = o.limits.LookupBurst
	}
	// Like Traefik does in production, the tests tell the server who the client
	// is with X-Forwarded-For: the per-IP upgrade bucket (20, refilled at 1 per
	// second) would otherwise be shared by every test client.
	trustXFF := !o.noForwardedFor
	httpOpts := httpapi.Options{
		Logger:            logger,
		Version:           "integration",
		Registry:          reg,
		Rooms:             hub,
		Guests:            guests,
		Sessions:          sessions,
		Google:            google,
		AllowedOrigins:    origins,
		TrustForwardedFor: trustXFF,
		Now:               clk.Now,
		Limits:            limits,
	}
	if backend != nil {
		httpOpts.Accounts = backend
	}
	api := httpapi.New(httpOpts)
	deps := ws.Deps{
		Rooms:             hub,
		Guests:            guests,
		Sessions:          sessions,
		AllowedOrigins:    origins,
		TrustForwardedFor: trustXFF,
		Logger:            logger,
		Registerer:        reg,
		Now:               o.wsNow,
	}
	if o.ws != nil {
		o.ws(&deps)
	}
	wsh := ws.NewHandler(deps)
	api.Mux().Handle("GET /ws", wsh)
	srv.Config.Handler = api.Handler()
	srv.Start()

	s := &stack{
		t: t, clock: clk, store: st, hub: hub, wsh: wsh, guests: guests, sessions: sessions, backend: backend, idp: o.idp,
		api: api, srv: srv, reg: reg,
		logs: logs, origin: origin, restored: restored,
		http: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	s.codes = snapshotCodes(t, st)
	t.Cleanup(s.close)
	return s
}

func (s *stack) track(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes = append(s.codes, code)
}

// flush returns once every room has processed what was posted to it before the
// call (Hub.Info is ordered behind the room's mailbox).
func (s *stack) flush() {
	s.mu.Lock()
	codes := append([]string(nil), s.codes...)
	s.mu.Unlock()
	for _, code := range codes {
		s.hub.Info(code)
	}
}

// advance moves the ManualClock and lets the rooms catch up with it. A
// ManualClock never fires a timer on its own, not even one created for an
// instant that has already passed; a room that handles a wake-up after the
// clock moved (the 5 s periodic snapshot, say) may arm such a timer, so after
// the move the due timers are fired again until the rooms are quiet.
func (s *stack) advance(d time.Duration) {
	s.clock.Advance(d)
	for range 3 {
		s.flush()
		s.clock.Advance(0)
	}
	s.flush()
}

// shutdownHub drains the hub like main.go does on SIGTERM (final snapshots,
// 1012 to every socket). It is idempotent.
func (s *stack) shutdownHub() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.hub.Shutdown(ctx)
}

// shutdown is the whole drain of cmd/server: the hub, then the WebSocket
// endpoint (which waits for every connection goroutine). Idempotent.
func (s *stack) shutdown() error {
	err := s.shutdownHub()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if werr := s.wsh.Shutdown(ctx); err == nil {
		err = werr
	}
	return err
}

// close shuts everything down and checks the server never recovered a panic.
func (s *stack) close() {
	s.closeOnce.Do(func() {
		if err := s.shutdown(); err != nil {
			s.t.Errorf("hub shutdown: %v", err)
		}
		s.srv.Close()
		if logs := s.logs.String(); strings.Contains(logs, "panicked") {
			s.t.Errorf("a room panicked:\n%s", logs)
		}
	})
}

func (s *stack) wsURL() string { return "ws" + strings.TrimPrefix(s.srv.URL, "http") + "/ws" }

// ---------------------------------------------------------------------------
// Goroutine leak check.
// ---------------------------------------------------------------------------

var leakRegistered sync.Map

var leakMarkers = []string{
	"/server/internal/room.",
	"/server/internal/ws.",
	"/server/internal/httpapi.",
	"/server/internal/store.",
	"integration.(*client)",
}

// registerLeakCheck makes sure that, once the test's other cleanups ran (it is
// registered by the first stack of the test, so it runs last), no goroutine of
// the server or of a client is left.
func registerLeakCheck(t *testing.T) {
	t.Helper()
	if _, loaded := leakRegistered.LoadOrStore(t, true); loaded {
		return
	}
	t.Cleanup(func() {
		leakRegistered.Delete(t)
		deadline := time.Now().Add(waitLimit)
		for {
			leaked := leakedGoroutines()
			if len(leaked) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("goroutines leaked after shutdown:\n%s", strings.Join(leaked, "\n\n"))
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

func leakedGoroutines() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []string
	for i, g := range strings.Split(string(buf), "\n\n") {
		if i == 0 { // the calling goroutine
			continue
		}
		for _, m := range leakMarkers {
			if strings.Contains(g, m) {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// eventually polls cond (store contents, gauges) for at most waitLimit.
func eventually(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", desc)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// REST helpers.
// ---------------------------------------------------------------------------

type player struct {
	name   string
	avatar game.Avatar
	cookie string // value of the Cookie header
	id     string // guest ID = player ID
}

func (s *stack) tryNewPlayer(name string) (*player, error) {
	req, err := http.NewRequest(http.MethodGet, s.srv.URL+"/api/session", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", s.origin)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/session = %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == protocol.GuestCookie {
			id, _, _ := strings.Cut(c.Value, ".")
			return &player{
				name:   name,
				avatar: game.Avatar{Emoji: "🍓", Color: "#ff0066"},
				cookie: (&http.Cookie{Name: c.Name, Value: c.Value}).String(),
				id:     id,
			}, nil
		}
	}
	return nil, errors.New("GET /api/session set no guest cookie")
}

// newPlayer gets a guest cookie from GET /api/session, like the web app does.
func (s *stack) newPlayer(name string) *player {
	s.t.Helper()
	p, err := s.tryNewPlayer(name)
	if err != nil {
		s.t.Fatal(err)
	}
	return p
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// jsonBody decodes the body into v.
func (r response) jsonBody(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decoding %q: %v", r.body, err)
	}
}

// errCode returns the "error" field of a REST error body.
func (r response) errCode(t *testing.T) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	r.jsonBody(t, &e)
	return e.Error
}

// readJSON decodes (and closes) the body of a raw http.Response.
func readJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response body: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decoding %q: %v", b, err)
	}
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withoutHeader(k string) reqOpt { return func(r *http.Request) { r.Header.Del(k) } }
func withCookie(p *player) reqOpt   { return withHeader("Cookie", p.cookie) }

func (s *stack) tryDo(method, path string, body []byte, opts ...reqOpt) (response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, rd)
	if err != nil {
		return response{}, err
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", s.origin)
	for _, o := range opts {
		o(req)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: b}, err
}

func (s *stack) do(method, path string, body []byte, opts ...reqOpt) response {
	s.t.Helper()
	r, err := s.tryDo(method, path, body, opts...)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

// tryCreateRoom is POST /api/rooms as p; it returns the room code.
func (s *stack) tryCreateRoom(p *player, lang string) (string, error) {
	body := map[string]any{"name": p.name, "avatar": p.avatar}
	if lang != "" {
		body["lang"] = lang
	}
	b, _ := json.Marshal(body)
	r, err := s.tryDo(http.MethodPost, "/api/rooms", b, withCookie(p))
	if err != nil {
		return "", err
	}
	if r.status != http.StatusCreated {
		return "", fmt.Errorf("POST /api/rooms = %d %s", r.status, r.body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		return "", err
	}
	s.track(out.Code)
	// The room arms its timers on its own goroutine right after Create; wait
	// for that, so that a clock advance right after the request cannot move
	// the room's idle deadline along with it.
	s.hub.Info(out.Code)
	return out.Code, nil
}

func (s *stack) createRoom(p *player, lang string) string {
	s.t.Helper()
	code, err := s.tryCreateRoom(p, lang)
	if err != nil {
		s.t.Fatal(err)
	}
	return code
}

// roomInfo is GET /api/rooms/{code}; ok is false on 404.
func (s *stack) roomInfo(code string) (room.Info, bool) {
	s.t.Helper()
	r := s.do(http.MethodGet, "/api/rooms/"+code, nil)
	switch r.status {
	case http.StatusOK:
		var info room.Info
		r.jsonBody(s.t, &info)
		return info, true
	case http.StatusNotFound:
		if got := r.errCode(s.t); got != protocol.ErrRoomNotFound {
			s.t.Fatalf("GET /api/rooms/%s 404 error = %q", code, got)
		}
		return room.Info{}, false
	}
	s.t.Fatalf("GET /api/rooms/%s = %d %s", code, r.status, r.body)
	return room.Info{}, false
}

// ---------------------------------------------------------------------------
// Metrics and store inspection.
// ---------------------------------------------------------------------------

// metric returns the value of a counter or gauge series of the stack's
// registry (labels must match exactly); ok is false when it does not exist.
func (s *stack) metric(name string, labels map[string]string) (float64, bool) {
	s.t.Helper()
	mfs, err := s.reg.Gather()
	if err != nil {
		s.t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	series:
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
					continue series
				}
			}
			switch {
			case m.Counter != nil:
				return m.Counter.GetValue(), true
			case m.Gauge != nil:
				return m.Gauge.GetValue(), true
			}
		}
	}
	return 0, false
}

func (s *stack) metricValue(name string, labels map[string]string) float64 {
	s.t.Helper()
	v, _ := s.metric(name, labels)
	return v
}

// labelValues lists every value of one label across the series of a metric.
func (s *stack) labelValues(name, label string) []string {
	s.t.Helper()
	mfs, err := s.reg.Gather()
	if err != nil {
		s.t.Fatal(err)
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

// snapshot loads the room's saved game from the store.
func snapshot(t *testing.T, st store.Store, code string) (*game.Game, bool) {
	t.Helper()
	snaps, err := st.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("store.LoadAll: %v", err)
	}
	for _, sn := range snaps {
		if sn.Code != code {
			continue
		}
		var p room.Persisted
		if err := json.Unmarshal(sn.State, &p); err != nil {
			t.Fatalf("decoding snapshot %s: %v", code, err)
		}
		var g game.Game
		if err := json.Unmarshal(p.Game, &g); err != nil {
			t.Fatalf("decoding game of snapshot %s: %v", code, err)
		}
		return &g, true
	}
	return nil, false
}

func snapshotCodes(t *testing.T, st store.Store) []string {
	t.Helper()
	snaps, err := st.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("store.LoadAll: %v", err)
	}
	var codes []string
	for _, sn := range snaps {
		codes = append(codes, sn.Code)
	}
	return codes
}
