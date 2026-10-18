// Package ws is the WebSocket endpoint of the game server: it authenticates
// the upgrade (guest cookie, Origin, connection caps, rate limit), then pumps
// JSON frames (protocol.Envelope) between the socket and a room.Session.
//
// Origin policy. The upgrade is refused (403) unless the Origin header matches
// one of Deps.AllowedOrigins (scheme, host and port; auth.CheckOrigin). A
// request without an Origin header is accepted only when AllowedOrigins is
// empty, i.e. in development with nothing configured, so curl-like clients and
// tests keep working; with any configured origin (production sets PUBLIC_URL)
// a missing Origin is refused, because every browser sends one on WebSocket
// upgrades. When AllowedOrigins is empty a present Origin must equal the
// request Host.
//
// Admission order. A request is checked cheapest-first and is only charged to
// the per-IP upgrade bucket once it will really be upgraded: guest cookie
// (401), Origin (403), well-formed upgrade request (405/426/400), server not
// shutting down or full (503), per-guest connection cap (429), and only then
// the per-IP bucket (429). Rejected requests therefore never consume the
// quota that legitimate upgrades behind the same IP (e.g. a shared reverse
// proxy) depend on.
//
// Lifecycle. A connection must send "join" within Deps.JoinTimeout and any
// frame within Deps.ReadIdleTimeout. Handler.Shutdown drains every open socket
// (joined or not); cmd/server calls it after Hub.Shutdown.
package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

// Upgrades per client IP (docs/DESIGN.md section 9a). Behind the homelab's
// Traefik Service every client shares one SNAT'd IP, so in production this
// bucket is effectively global: it is sized so that a group of friends and a
// reconnect storm after a deploy never hit it, while still capping a flood.
const (
	upgradeRate  = rate.Limit(300.0 / 60) // 300 per minute...
	upgradeBurst = 60                     // ...in bursts of 60
	// limiterIdleTTL is how long an idle IP keeps its bucket.
	limiterIdleTTL = 10 * time.Minute
)

// Connection caps.
const (
	// DefaultMaxConns is the default for Deps.MaxConns: the number of live
	// sockets (joined or not) the process serves at once.
	DefaultMaxConns = 2000
	// DefaultMaxConnsPerGuest is the default for Deps.MaxConnsPerGuest. A
	// player has one socket, two during a reconnect race.
	DefaultMaxConnsPerGuest = 4
)

const (
	// busyRetryAfter is the Retry-After (seconds) of cap and shutdown refusals.
	busyRetryAfter = "5"
	// defaultCloseGrace bounds how long a server-initiated close may take
	// (flushing queued frames plus the peer's close frame) before the
	// connection is torn down regardless.
	defaultCloseGrace = 5 * time.Second
	// defaultFrameTimeout bounds the time to receive the rest of a frame once
	// its header arrived.
	defaultFrameTimeout = 10 * time.Second
)

// roomCodeAlphabet is the alphabet of room codes (see internal/room).
const roomCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ23456789"

// Deps are the dependencies of the WebSocket handler.
type Deps struct {
	// Rooms attaches connections to rooms. Required.
	Rooms room.Rooms
	// Guests identifies the player from the guest cookie. Required.
	Guests *auth.Guests
	// Sessions, when set, reads the account cookie of the upgrade request: a
	// valid session links the player to that user (statistics, see
	// room.UserLinked). An invalid or missing session just means a guest: it
	// is never an error.
	Sessions *auth.Sessions
	// AllowedOrigins is PUBLIC_URL's origin merged with ALLOWED_ORIGINS
	// (config.Config.Origins). See the package documentation.
	AllowedOrigins []string
	// TrustForwardedFor takes the client IP (rate limiting, logs) from the
	// rightmost X-Forwarded-For entry instead of the TCP peer.
	TrustForwardedFor bool
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Registerer receives tuttifrutti_ws_closes_total,
	// tuttifrutti_ws_frames_total and tuttifrutti_ws_connections_open; nil
	// disables the metrics.
	Registerer prometheus.Registerer
	// ReadIdleTimeout closes a connection that sent no frame for this long
	// (default protocol.ReadIdleTimeout).
	ReadIdleTimeout time.Duration
	// WriteTimeout bounds one frame write (default protocol.WriteTimeout).
	WriteTimeout time.Duration
	// UpgradeRate and UpgradeBurst bound the upgrades per client IP; zero
	// selects the default (300 per minute, bursts of 60).
	UpgradeRate  rate.Limit
	UpgradeBurst int
	// JoinTimeout closes (ClosePolicy, "join timeout") a connection whose
	// "join" was not accepted by Rooms.Connect within this time; pings do not
	// extend it, and it stops once Connect succeeded (the room layer then owns
	// the seat's deadline). Default protocol.JoinTimeout.
	JoinTimeout time.Duration
	// MaxConns caps the live sockets, joined or not. Beyond it upgrades get
	// 503 server_busy before the upgrade. Default DefaultMaxConns.
	MaxConns int
	// MaxConnsPerGuest caps the live sockets of one guest ID; beyond it
	// upgrades get 429 too_many_connections. Default DefaultMaxConnsPerGuest.
	MaxConnsPerGuest int
	// Now is the clock for pong timestamps and rate limits (default time.Now).
	Now func() time.Time
}

// Handler serves GET /ws. It implements http.Handler; mount it on the mux and
// call Shutdown while draining the server.
type Handler struct {
	rooms            room.Rooms
	guests           *auth.Guests
	sessions         *auth.Sessions
	origins          []string
	trustXFF         bool
	log              *slog.Logger
	m                *metrics
	readIdleTimeout  time.Duration
	writeTimeout     time.Duration
	joinTimeout      time.Duration
	upgradeRate      rate.Limit
	upgradeBurst     int
	maxConns         int
	maxConnsPerGuest int
	now              func() time.Time
	limiter          *ratelimit.Keyed

	// Tunables that tests shrink before serving.
	closeGrace   time.Duration
	frameTimeout time.Duration

	// restartFrame is the pre-encoded "event server_restarting" frame.
	restartFrame []byte

	mu           sync.Mutex
	total        int            // live sockets, from admission to the end of the pumps
	perGuest     map[string]int // live sockets by guest ID
	conns        map[*conn]struct{}
	shuttingDown bool
	// forced is set when Shutdown's context expired: connections registered
	// afterwards (upgrades admitted before Shutdown that finish Accept late)
	// are torn down immediately instead of being drained politely.
	forced      bool
	drained     chan struct{} // closed once shutting down and total == 0
	drainedOnce sync.Once
}

// NewHandler returns the handler to mount on GET /ws. It panics when Rooms or
// Guests is nil (a programming error).
func NewHandler(d Deps) *Handler {
	if d.Rooms == nil || d.Guests == nil {
		panic("ws: Deps.Rooms and Deps.Guests are required")
	}
	h := &Handler{
		rooms:            d.Rooms,
		guests:           d.Guests,
		sessions:         d.Sessions,
		origins:          d.AllowedOrigins,
		trustXFF:         d.TrustForwardedFor,
		log:              d.Logger,
		m:                newMetrics(d.Registerer),
		readIdleTimeout:  d.ReadIdleTimeout,
		writeTimeout:     d.WriteTimeout,
		joinTimeout:      d.JoinTimeout,
		upgradeRate:      d.UpgradeRate,
		upgradeBurst:     d.UpgradeBurst,
		maxConns:         d.MaxConns,
		maxConnsPerGuest: d.MaxConnsPerGuest,
		now:              d.Now,
		closeGrace:       defaultCloseGrace,
		frameTimeout:     defaultFrameTimeout,
		perGuest:         make(map[string]int),
		conns:            make(map[*conn]struct{}),
		drained:          make(chan struct{}),
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.readIdleTimeout <= 0 {
		h.readIdleTimeout = protocol.ReadIdleTimeout
	}
	if h.writeTimeout <= 0 {
		h.writeTimeout = protocol.WriteTimeout
	}
	if h.joinTimeout <= 0 {
		h.joinTimeout = protocol.JoinTimeout
	}
	if h.upgradeRate <= 0 {
		h.upgradeRate = upgradeRate
	}
	if h.upgradeBurst <= 0 {
		h.upgradeBurst = upgradeBurst
	}
	if h.maxConns <= 0 {
		h.maxConns = DefaultMaxConns
	}
	if h.maxConnsPerGuest <= 0 {
		h.maxConnsPerGuest = DefaultMaxConnsPerGuest
	}
	if h.now == nil {
		h.now = time.Now
	}
	h.limiter = ratelimit.NewKeyed(h.upgradeRate, h.upgradeBurst, limiterIdleTTL, h.now)
	h.restartFrame = encodeRestartFrame()
	return h
}

func encodeRestartFrame() []byte {
	d, _ := json.Marshal(game.Event{Type: protocol.EventServerRestarting})
	frame, _ := json.Marshal(protocol.Envelope{T: protocol.TypeEvent, V: protocol.Version, D: d})
	return frame
}

// Open returns the number of live sockets (joined or not).
func (h *Handler) Open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := ratelimit.ClientIP(r, h.trustXFF)
	id, ok := h.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, protocol.ErrUnauthorized)
		return
	}
	if !auth.CheckOrigin(r, h.origins, len(h.origins) == 0) {
		writeError(w, http.StatusForbidden, "forbidden_origin")
		return
	}
	if !checkUpgrade(w, r) {
		return
	}

	switch h.acquire(id) {
	case admitBusy:
		writeBusy(w, http.StatusServiceUnavailable, protocol.ErrServerBusy)
		return
	case admitGuestCap:
		writeBusy(w, http.StatusTooManyRequests, "too_many_connections")
		return
	}
	defer h.release(id)

	// Only requests that will be upgraded are charged to the shared bucket.
	if ok, retry := h.limiter.Allow(ratelimit.KeyForIP(ip)); !ok {
		ratelimit.Reject(w, retry)
		return
	}

	// The Origin was verified above (the library's own check only knows host
	// patterns, not schemes), so its check is skipped. Compression stays off:
	// frames are tiny and it costs memory per connection.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Debug("ws upgrade failed", "ip", ip, "err", err)
		return
	}
	ws.SetReadLimit(protocol.MaxMessageBytes)

	c := newConn(h, ws, id, ip)
	if h.sessions != nil {
		c.userID, _ = h.sessions.Identify(r)
	}
	shuttingDown, forced := h.register(c)
	defer h.unregister(c)
	if shuttingDown {
		// Shutdown started between admission and now: Shutdown's snapshot
		// does not contain this connection, so drain it ourselves, and if
		// Shutdown already ran out of time, do not wait for a polite close.
		c.shutdown()
		if forced {
			c.cancel()
		}
	}
	c.run()
}

type admission int

const (
	admitOK admission = iota
	// admitBusy: shutting down, or MaxConns reached.
	admitBusy
	// admitGuestCap: MaxConnsPerGuest reached.
	admitGuestCap
)

// acquire reserves a socket slot for guest, or says why it cannot.
func (h *Handler) acquire(guest string) admission {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.shuttingDown, h.total >= h.maxConns:
		return admitBusy
	case h.perGuest[guest] >= h.maxConnsPerGuest:
		return admitGuestCap
	}
	h.total++
	h.perGuest[guest]++
	h.m.opened()
	return admitOK
}

// release gives back a slot taken by acquire. It runs exactly once per
// admitted request (deferred in ServeHTTP), whatever the exit path.
func (h *Handler) release(guest string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total--
	if n := h.perGuest[guest] - 1; n > 0 {
		h.perGuest[guest] = n
	} else {
		delete(h.perGuest, guest)
	}
	h.m.released()
	if h.shuttingDown && h.total == 0 {
		h.drainedOnce.Do(func() { close(h.drained) })
	}
}

// register tracks an accepted connection for Shutdown. It reports whether the
// handler is already shutting down, and whether Shutdown already gave up
// waiting (its context expired), in which case the caller must tear the
// connection down at once.
func (h *Handler) register(c *conn) (shuttingDown, forced bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c] = struct{}{}
	return h.shuttingDown, h.forced
}

func (h *Handler) unregister(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

func (h *Handler) snapshot() []*conn {
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	return conns
}

// Shutdown drains the endpoint. It stops accepting upgrades (503 server_busy),
// sends event server_restarting to every open socket that has not received
// one, closes all of them with protocol.CloseServiceRestart, and waits until
// every connection goroutine has exited. Sockets the room layer already
// started closing keep the close code they were given. When ctx ends first the
// remaining connections are torn down without further politeness and
// ctx.Err() is returned. It is idempotent and safe to call concurrently with
// connection teardown.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.shuttingDown = true
	conns := h.snapshot()
	if h.total == 0 {
		h.drainedOnce.Do(func() { close(h.drained) })
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.shutdown()
	}

	select {
	case <-h.drained:
		return nil
	case <-ctx.Done():
		h.mu.Lock()
		// Registered after this point => register reports forced and the
		// connection tears itself down; registered before => in the snapshot.
		h.forced = true
		conns = h.snapshot()
		h.mu.Unlock()
		for _, c := range conns {
			c.cancel()
		}
		return ctx.Err()
	}
}

// checkUpgrade verifies that r is a well-formed WebSocket handshake, with the
// same statuses as the library, so that Accept cannot fail on the request
// itself and a request charged to the rate limiter is one that is upgraded.
func checkUpgrade(w http.ResponseWriter, r *http.Request) bool {
	refuse := func(status int, msg string) bool {
		http.Error(w, msg, status)
		return false
	}
	upgradeHeaders := func() {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
	}
	switch {
	case !r.ProtoAtLeast(1, 1):
		return refuse(http.StatusUpgradeRequired, "WebSocket handshake must use HTTP/1.1 or later")
	case !headerHasToken(r.Header, "Connection", "Upgrade"):
		upgradeHeaders()
		return refuse(http.StatusUpgradeRequired, "WebSocket protocol violation: Connection header does not contain Upgrade")
	case !headerHasToken(r.Header, "Upgrade", "websocket"):
		upgradeHeaders()
		return refuse(http.StatusUpgradeRequired, "WebSocket protocol violation: Upgrade header does not contain websocket")
	case r.Method != http.MethodGet:
		return refuse(http.StatusMethodNotAllowed, "WebSocket protocol violation: handshake request method is not GET")
	case r.Header.Get("Sec-WebSocket-Version") != "13":
		w.Header().Set("Sec-WebSocket-Version", "13")
		return refuse(http.StatusBadRequest, "unsupported WebSocket protocol version (only 13 is supported)")
	}
	keys := r.Header.Values("Sec-WebSocket-Key")
	switch {
	case len(keys) == 0:
		return refuse(http.StatusBadRequest, "WebSocket protocol violation: missing Sec-WebSocket-Key")
	case len(keys) > 1:
		return refuse(http.StatusBadRequest, "WebSocket protocol violation: multiple Sec-WebSocket-Key headers")
	}
	if k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0])); err != nil || len(k) != 16 {
		return refuse(http.StatusBadRequest, "WebSocket protocol violation: invalid Sec-WebSocket-Key")
	}
	return true
}

// headerHasToken reports whether any value of header key lists token in its
// comma-separated tokens (case-insensitive).
func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

func writeError(w http.ResponseWriter, status int, code string) {
	body, _ := json.Marshal(map[string]string{"error": code})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// writeBusy answers a refusal that is worth retrying shortly.
func writeBusy(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Retry-After", busyRetryAfter)
	writeError(w, status, code)
}

// normalizeCode trims s, upper-cases its ASCII letters and reports whether the
// result is a well-formed room code (4 characters of the code alphabet).
func normalizeCode(s string) (string, bool) {
	b := []byte(strings.TrimSpace(s))
	if len(b) != 4 {
		return "", false
	}
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
			b[i] = c
		}
		if strings.IndexByte(roomCodeAlphabet, c) < 0 {
			return "", false
		}
	}
	return string(b), true
}
