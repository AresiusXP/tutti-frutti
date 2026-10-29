// Package httpapi wires the HTTP surface of the server: health probes,
// Prometheus metrics and the REST API under /api.
//
// The WebSocket endpoint is not part of this package: cmd/server builds it
// with ws.NewHandler and mounts it on Mux() (api.Mux().Handle("GET /ws", h)),
// so it goes through the same logging and metrics middleware.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Per-client-IP limits (docs/DESIGN.md section 9a). Behind the homelab's
// Traefik Service (externalTrafficPolicy=Cluster) every client shares one
// SNAT'd IP, so in production these buckets are effectively GLOBAL: they are
// sized so that a group of friends (and a reconnect storm after a deploy) never
// hits them, while still capping a flood. Room creation is further bounded by
// the hub's MaxRooms and the 2-minute unclaimed-room TTL.
const (
	createRate  = rate.Limit(30.0 / 60)  // POST /api/rooms: 30 per minute...
	createBurst = 10                     // ...in bursts of 10
	lookupRate  = rate.Limit(300.0 / 60) // GET /api/rooms/{code}: 300 per minute...
	lookupBurst = 60                     // ...in bursts of 60
	// Browsing has its own budget: polling cannot starve preflight/reconnects.
	browseRate  = rate.Limit(300.0 / 60)
	browseBurst = 60
	// The login and callback endpoints share one bucket (each login spends two
	// tokens): 60 per minute in bursts of 30 lets a dozen friends log in at
	// once, while a flood costs us nothing but redirects.
	authRate  = rate.Limit(60.0 / 60)
	authBurst = 30

	// limiterIdleTTL is how long an idle IP keeps its bucket.
	limiterIdleTTL = 10 * time.Minute
	// readyCheckTimeout bounds all /readyz dependency checks together.
	readyCheckTimeout = 2 * time.Second
)

// Options configures a Server. Everything but Logger and Version is optional:
// the REST routes below are only registered when their dependencies are set.
type Options struct {
	Logger  *slog.Logger
	Version string

	// Rooms serves GET /api/rooms/{code} and, together with Guests,
	// POST /api/rooms.
	Rooms room.Rooms
	// Guests mints and verifies the guest cookie: it enables GET /api/session
	// and POST /api/rooms.
	Guests *auth.Guests
	// Sessions and Accounts enable the account endpoints (/api/me...,
	// /api/auth/...); GET /api/session reports loggedIn when Sessions is set.
	Sessions *auth.Sessions
	Accounts store.Accounts
	// Google runs the Google login; nil answers 404 auth_disabled on the login
	// endpoints and googleEnabled=false on GET /api/session.
	Google *auth.Google
	// AllowedOrigins are the origins that may call unsafe methods under /api
	// (PUBLIC_URL's origin already merged with ALLOWED_ORIGINS by the caller;
	// see config.Config.Origins). Only enforced when Rooms or Guests is set.
	AllowedOrigins []string
	// TrustForwardedFor takes the rate-limit client IP from the rightmost
	// X-Forwarded-For entry instead of the TCP peer.
	TrustForwardedFor bool
	// ReadyChecks run on every /readyz (2 s budget in total); the first
	// failure makes it answer 503. Liveness (/healthz) is unaffected.
	ReadyChecks []func(context.Context) error
	// Now is the clock of the rate limiters (default time.Now).
	Now func() time.Time
	// Limits overrides the default per-client-IP rate limits; zero fields keep
	// the defaults (createRate/createBurst, lookupRate/lookupBurst).
	Limits Limits
	// Registry is the Prometheus registry served on /metrics. Pass one when
	// other components (room.Hub, ws.Handler) must register their collectors
	// before the Server exists; it must not already hold the Go and process
	// collectors, which New registers. Default: a new registry.
	Registry *prometheus.Registry
}

// Limits are the per-client-IP rate limits of the REST routes. A zero field
// selects the default.
type Limits struct {
	// CreateRate and CreateBurst bound POST /api/rooms.
	CreateRate  rate.Limit
	CreateBurst int
	// LookupRate and LookupBurst bound GET /api/rooms/{code}.
	LookupRate  rate.Limit
	LookupBurst int
	// BrowseRate and BrowseBurst bound GET /api/rooms independently of lookup.
	BrowseRate  rate.Limit
	BrowseBurst int
	// AuthRate and AuthBurst bound GET /api/auth/google/login and /callback
	// together.
	AuthRate  rate.Limit
	AuthBurst int
}

func (l Limits) withDefaults() Limits {
	if l.CreateRate <= 0 {
		l.CreateRate = createRate
	}
	if l.CreateBurst <= 0 {
		l.CreateBurst = createBurst
	}
	if l.LookupRate <= 0 {
		l.LookupRate = lookupRate
	}
	if l.LookupBurst <= 0 {
		l.LookupBurst = lookupBurst
	}
	if l.BrowseRate <= 0 {
		l.BrowseRate = browseRate
	}
	if l.BrowseBurst <= 0 {
		l.BrowseBurst = browseBurst
	}
	if l.AuthRate <= 0 {
		l.AuthRate = authRate
	}
	if l.AuthBurst <= 0 {
		l.AuthBurst = authBurst
	}
	return l
}

// Server holds the HTTP handlers and their shared state.
type Server struct {
	log      *slog.Logger
	version  string
	ready    atomic.Bool
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	mux      *http.ServeMux

	rooms       room.Rooms
	guests      *auth.Guests
	sessions    *auth.Sessions
	accounts    store.Accounts
	google      *auth.Google
	logins      *prometheus.CounterVec
	origins     []string
	trustXFF    bool
	readyChecks []func(context.Context) error
	createLimit *ratelimit.Keyed
	lookupLimit *ratelimit.Keyed
	browseLimit *ratelimit.Keyed
	authLimit   *ratelimit.Keyed
}

// New builds a Server. It starts ready; call SetReady(false) when shutting
// down so Kubernetes stops routing traffic before connections are drained.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	lim := opts.Limits.withDefaults()
	s := &Server{
		log:         opts.Logger,
		version:     opts.Version,
		registry:    opts.Registry,
		mux:         http.NewServeMux(),
		rooms:       opts.Rooms,
		guests:      opts.Guests,
		sessions:    opts.Sessions,
		accounts:    opts.Accounts,
		google:      opts.Google,
		logins:      newLoginMetric(),
		origins:     opts.AllowedOrigins,
		trustXFF:    opts.TrustForwardedFor,
		readyChecks: opts.ReadyChecks,
		createLimit: ratelimit.NewKeyed(lim.CreateRate, lim.CreateBurst, limiterIdleTTL, opts.Now),
		lookupLimit: ratelimit.NewKeyed(lim.LookupRate, lim.LookupBurst, limiterIdleTTL, opts.Now),
		browseLimit: ratelimit.NewKeyed(lim.BrowseRate, lim.BrowseBurst, limiterIdleTTL, opts.Now),
		authLimit:   ratelimit.NewKeyed(lim.AuthRate, lim.AuthBurst, limiterIdleTTL, opts.Now),
	}
	if s.registry == nil {
		s.registry = prometheus.NewRegistry()
	}
	s.ready.Store(true)

	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tuttifrutti_http_requests_total",
		Help: "HTTP requests by route pattern, method and status code.",
	}, []string{"route", "method", "code"})
	s.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tuttifrutti_http_request_duration_seconds",
		Help:    "HTTP request latency by route pattern and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		s.requests,
		s.duration,
		s.logins,
	)

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.registerREST()
	s.registerAccounts()
	s.mux.HandleFunc("/api/", handleAPINotFound)
	return s
}

// Handler returns the root handler (with logging and metrics middleware).
func (s *Server) Handler() http.Handler {
	return s.instrument(secureHeaders(s.guardOrigin(s.mux)))
}

// Mux exposes the underlying mux so other packages can register routes; cmd/server
// uses it to mount the WebSocket handler: api.Mux().Handle("GET /ws", ws.NewHandler(...)).
// Register routes before serving; the mux is not safe to change while serving.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Registry exposes the Prometheus registry for custom collectors.
func (s *Server) Registry() *prometheus.Registry { return s.registry }

// SetReady flips the readiness state reported by /readyz.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("shutting down"))
		return
	}
	if len(s.readyChecks) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()
		for i, check := range s.readyChecks {
			if err := check(ctx); err != nil {
				s.log.Warn("readiness check failed", "check", i, "err", err)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("not ready"))
				return
			}
		}
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func handleAPINotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers {"error":"<code>"}.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// secureHeaders adds the headers every response carries: no content sniffing,
// no referrer to other origins, and, under /api, no caching unless the handler
// says otherwise (it overrides the default by setting its own Cache-Control).
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// guardOrigin rejects unsafe-method requests under /api whose Origin header is
// missing or not allowed (CSRF defence in depth on top of SameSite=Lax). It is
// only active when the REST API (Rooms, Guests or Sessions) is configured.
func (s *Server) guardOrigin(next http.Handler) http.Handler {
	if s.rooms == nil && s.guests == nil && s.sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") &&
			!auth.CheckOrigin(r, s.origins, false) {
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusForbidden, "forbidden_origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// methodLabel bounds the cardinality of the method metric label: the method is
// client-controlled, so anything that is not a standard verb becomes "OTHER".
func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

// statusRecorder captures the response status. It deliberately implements
// Unwrap so http.ResponseController (and later WebSocket upgrades via
// Hijacker) can reach the underlying writer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// instrument records metrics and logs every request. Probe and scrape
// endpoints are logged at debug level to keep the logs readable.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// r.Pattern is populated in place by ServeMux while dispatching.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		method := methodLabel(r.Method)
		s.requests.WithLabelValues(route, method, strconv.Itoa(rec.status)).Inc()
		// A hijacked WebSocket lives for minutes: its lifetime is not a
		// request latency.
		if rec.status != http.StatusSwitchingProtocols {
			s.duration.WithLabelValues(route, method).Observe(elapsed.Seconds())
		}

		level := slog.LevelInfo
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "http request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", elapsed.Milliseconds())
	})
}
