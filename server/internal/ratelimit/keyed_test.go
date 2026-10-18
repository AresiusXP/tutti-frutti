package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestKeyedBurstAndRefill(t *testing.T) {
	clk := newClock()
	// 10 per minute, burst 5 (the room-creation limit).
	k := NewKeyed(rate.Every(6*time.Second), 5, time.Minute, clk.Now)

	for i := 0; i < 5; i++ {
		if ok, _ := k.Allow("a"); !ok {
			t.Fatalf("request %d within burst was refused", i+1)
		}
	}
	ok, retry := k.Allow("a")
	if ok {
		t.Fatal("6th request must be refused")
	}
	if retry != 6*time.Second {
		t.Errorf("retryAfter = %v, want 6s", retry)
	}
	// Refusals must not push the next token further away.
	ok, retry = k.Allow("a")
	if ok || retry != 6*time.Second {
		t.Errorf("repeated refusal = %v, %v; want false, 6s", ok, retry)
	}

	clk.Advance(5 * time.Second)
	if ok, retry := k.Allow("a"); ok || retry != time.Second {
		t.Errorf("after 5s: %v, %v; want false, 1s", ok, retry)
	}
	clk.Advance(time.Second)
	if ok, _ := k.Allow("a"); !ok {
		t.Error("one token must be back after 6s")
	}
	if ok, _ := k.Allow("a"); ok {
		t.Error("only one token should have been refilled")
	}
}

func TestKeyedKeysAreIndependent(t *testing.T) {
	clk := newClock()
	k := NewKeyed(1, 2, time.Minute, clk.Now)
	for i := 0; i < 2; i++ {
		k.Allow("a")
	}
	if ok, _ := k.Allow("a"); ok {
		t.Fatal("a must be exhausted")
	}
	if ok, _ := k.Allow("b"); !ok {
		t.Error("b must have its own bucket")
	}
}

func TestKeyedClampsConfig(t *testing.T) {
	k := NewKeyed(1, 0, 0, nil)
	if ok, _ := k.Allow("a"); !ok {
		t.Error("burst 0 must be clamped to 1")
	}
	if ok, _ := k.Allow("a"); ok {
		t.Error("second request must be refused with burst 1")
	}
	if k.ttl != defaultIdleTTL {
		t.Errorf("ttl = %v, want default %v", k.ttl, defaultIdleTTL)
	}
}

func TestKeyedIdleTTLCoversRefill(t *testing.T) {
	// A bucket needs 20s to refill; an idleTTL of 1s must not let a client
	// get a fresh bucket by pausing for a few seconds.
	clk := newClock()
	k := NewKeyed(1, 20, time.Second, clk.Now)
	if k.ttl < 20*time.Second {
		t.Fatalf("ttl = %v, want >= 20s", k.ttl)
	}
	for i := 0; i < 20; i++ {
		if ok, _ := k.Allow("a"); !ok {
			t.Fatalf("burst request %d refused", i+1)
		}
	}
	clk.Advance(10 * time.Second)
	k.Allow("other") // triggers a sweep
	if k.Len() != 2 {
		t.Fatalf("Len = %d, want 2: the exhausted bucket must survive", k.Len())
	}
	granted := 0
	for i := 0; i < 20; i++ {
		if ok, _ := k.Allow("a"); ok {
			granted++
		}
	}
	if granted != 10 {
		t.Errorf("granted %d after a 10s pause, want 10 (1 token/s)", granted)
	}
}

func TestKeyedZeroRate(t *testing.T) {
	k := NewKeyed(0, 1, time.Minute, nil)
	if ok, _ := k.Allow("a"); !ok {
		t.Error("the first burst token must be granted")
	}
	if ok, retry := k.Allow("a"); ok || retry <= 0 {
		t.Errorf("exhausted zero-rate bucket = %v, %v; want false, >0", ok, retry)
	}
}

func TestKeyedEvictsIdleKeys(t *testing.T) {
	clk := newClock()
	k := NewKeyed(10, 5, time.Minute, clk.Now)
	k.Allow("old")
	clk.Advance(30 * time.Second)
	k.Allow("recent")
	if k.Len() != 2 {
		t.Fatalf("Len = %d, want 2", k.Len())
	}

	clk.Advance(45 * time.Second) // old idle 75s, recent idle 45s
	k.Allow("trigger")
	if k.Len() != 2 {
		t.Errorf("Len = %d, want 2 (old evicted, recent and trigger kept)", k.Len())
	}

	clk.Advance(2 * time.Minute)
	k.Allow("trigger")
	if k.Len() != 1 {
		t.Errorf("Len = %d, want 1 after everything else idled out", k.Len())
	}
}

func TestKeyedMemoryStaysBounded(t *testing.T) {
	clk := newClock()
	k := NewKeyed(1, 1, time.Minute, clk.Now)
	for i := 0; i < 100_000; i++ {
		k.Allow("ip-" + strconv.Itoa(i))
	}
	if k.Len() != 100_000 {
		t.Fatalf("Len = %d, want 100000 before eviction", k.Len())
	}
	clk.Advance(2 * time.Minute)
	k.Allow("late")
	if got := k.Len(); got > 1 {
		t.Errorf("Len = %d after eviction, want 1", got)
	}
}

func TestKeyedSteadyStateIsBounded(t *testing.T) {
	// 1 new key per second for an hour with a 1-minute TTL: never more than
	// ~1.5 x TTL of keys alive.
	clk := newClock()
	k := NewKeyed(1, 1, time.Minute, clk.Now)
	maxLen := 0
	for i := 0; i < 3600; i++ {
		k.Allow("ip-" + strconv.Itoa(i))
		clk.Advance(time.Second)
		if n := k.Len(); n > maxLen {
			maxLen = n
		}
	}
	if maxLen > 100 {
		t.Errorf("max tracked keys = %d, want <= 100 (1.5 x 60s)", maxLen)
	}
}

func TestKeyedConcurrent(t *testing.T) {
	k := NewKeyed(1000, 100, time.Minute, nil)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if ok, _ := k.Allow(fmt.Sprint("k", i%4)); ok {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if allowed.Load() == 0 {
		t.Error("nothing was allowed")
	}
	if k.Len() != 4 {
		t.Errorf("Len = %d, want 4", k.Len())
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 1},
		{-time.Second, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Millisecond, 2},
		{5900 * time.Millisecond, 6},
		{6 * time.Second, 6},
		{time.Hour, 3600},
	}
	for _, tt := range tests {
		if got := RetryAfterSeconds(tt.d); got != tt.want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	clk := newClock()
	k := NewKeyed(rate.Every(6*time.Second), 2, time.Minute, clk.Now)
	var served atomic.Int32
	h := Middleware(k, func(r *http.Request) string { return r.Header.Get("X-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			served.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))

	do := func(key string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Key", key)
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := do("a"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d = %d, want 204", i+1, rec.Code)
		}
	}
	rec := do("a")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "6" {
		t.Errorf("Retry-After = %q, want 6", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"rate_limited"}` {
		t.Errorf("body = %q", body)
	}
	if served.Load() != 2 {
		t.Errorf("downstream served %d requests, want 2", served.Load())
	}
	if rec := do("b"); rec.Code != http.StatusNoContent {
		t.Errorf("other key = %d, want 204", rec.Code)
	}

	clk.Advance(1500 * time.Millisecond)
	if rec := do("a"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "5" {
		t.Errorf("after 1.5s: %d Retry-After=%q, want 429 and 5 (4.5s rounded up)", rec.Code, rec.Header().Get("Retry-After"))
	}
}
