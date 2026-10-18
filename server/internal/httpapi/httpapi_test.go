package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *Server {
	return New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "1.2.3"})
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthAndReadiness(t *testing.T) {
	s := newTestServer()
	h := s.Handler()

	if rec := do(h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("/healthz = %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("/readyz ready = %d", rec.Code)
	}
	s.SetReady(false)
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz shutting down = %d", rec.Code)
	}
	// Liveness must stay green while draining.
	if rec := do(h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz while draining = %d", rec.Code)
	}
}

func TestVersionAndAPINotFound(t *testing.T) {
	h := newTestServer().Handler()

	rec := do(h, http.MethodGet, "/api/version")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":"1.2.3"`) {
		t.Errorf("/api/version = %d %q", rec.Code, rec.Body.String())
	}
	rec = do(h, http.MethodGet, "/api/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("/api/nope = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestMetrics(t *testing.T) {
	h := newTestServer().Handler()
	do(h, http.MethodGet, "/api/version")

	rec := do(h, http.MethodGet, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"go_goroutines",
		`tuttifrutti_http_requests_total{code="200",method="GET",route="GET /api/version"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

func TestMetricsMethodLabelIsBounded(t *testing.T) {
	h := newTestServer().Handler()
	for _, m := range []string{"FOO", "BAR", "X-ANYTHING"} {
		do(h, m, "/api/version")
	}

	body := do(h, http.MethodGet, "/metrics").Body.String()
	if !strings.Contains(body, `method="OTHER"`) {
		t.Error("non-standard methods must be counted as OTHER")
	}
	for _, leaked := range []string{`method="FOO"`, `method="BAR"`, `method="X-ANYTHING"`} {
		if strings.Contains(body, leaked) {
			t.Errorf("/metrics leaks client-controlled label %s", leaked)
		}
	}
}
