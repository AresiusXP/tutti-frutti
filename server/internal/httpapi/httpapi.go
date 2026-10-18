// Package httpapi wires the HTTP surface of the server: health probes,
// Prometheus metrics and the REST API under /api.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Options configures a Server.
type Options struct {
	Logger  *slog.Logger
	Version string
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
}

// New builds a Server. It starts ready; call SetReady(false) when shutting
// down so Kubernetes stops routing traffic before connections are drained.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{
		log:      opts.Logger,
		version:  opts.Version,
		registry: prometheus.NewRegistry(),
		mux:      http.NewServeMux(),
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
	)

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.mux.HandleFunc("/api/", handleAPINotFound)
	return s
}

// Handler returns the root handler (with logging and metrics middleware).
func (s *Server) Handler() http.Handler { return s.instrument(s.mux) }

// Mux exposes the underlying mux so other packages can register routes.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Registry exposes the Prometheus registry for custom collectors.
func (s *Server) Registry() *prometheus.Registry { return s.registry }

// SetReady flips the readiness state reported by /readyz.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("shutting down"))
		return
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
		s.duration.WithLabelValues(route, method).Observe(elapsed.Seconds())

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
