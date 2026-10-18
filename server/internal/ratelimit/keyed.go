// Package ratelimit provides per-key token buckets with bounded memory and
// trusted client-IP extraction, used to throttle room creation, room lookups
// and WebSocket upgrades per client IP.
package ratelimit

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// defaultIdleTTL is used when NewKeyed gets a non-positive idleTTL.
const defaultIdleTTL = 10 * time.Minute

// Keyed is a set of token-bucket limiters indexed by key (typically a client
// IP). Buckets that were not used for idleTTL are evicted lazily by Allow, so
// memory is bounded by the number of distinct keys seen within roughly 1.5 x
// idleTTL. There is no background goroutine, hence nothing to Close. It is
// safe for concurrent use.
type Keyed struct {
	rps   rate.Limit
	burst int
	ttl   time.Duration
	now   func() time.Time

	mu        sync.Mutex
	entries   map[string]*entry
	nextSweep time.Time
}

type entry struct {
	lim  *rate.Limiter
	last time.Time
}

// NewKeyed returns a limiter that lets each key do rps events per second with
// bursts of up to burst. Keys idle for idleTTL are forgotten; idleTTL is raised
// to the time a bucket needs to refill completely so that forgetting a key
// never grants more than the configured rate. now defaults to time.Now.
func NewKeyed(rps rate.Limit, burst int, idleTTL time.Duration, now func() time.Time) *Keyed {
	if burst < 1 {
		burst = 1
	}
	if now == nil {
		now = time.Now
	}
	if idleTTL <= 0 {
		idleTTL = defaultIdleTTL
	}
	if rps > 0 && !math.IsInf(float64(rps), 1) {
		refill := time.Duration(float64(burst) / float64(rps) * float64(time.Second))
		if refill > idleTTL {
			idleTTL = refill
		}
	}
	return &Keyed{rps: rps, burst: burst, ttl: idleTTL, now: now, entries: make(map[string]*entry)}
}

// Allow consumes one token of key's bucket. When the bucket is empty it
// returns false and how long the caller must wait for the next token.
func (k *Keyed) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := k.now()

	k.mu.Lock()
	defer k.mu.Unlock()

	if !now.Before(k.nextSweep) {
		k.sweep(now)
	}
	e := k.entries[key]
	if e == nil {
		e = &entry{lim: rate.NewLimiter(k.rps, k.burst)}
		k.entries[key] = e
	}
	e.last = now

	r := e.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, k.ttl
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// Len returns the number of keys currently tracked.
func (k *Keyed) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.entries)
}

func (k *Keyed) sweep(now time.Time) {
	for key, e := range k.entries {
		if now.Sub(e.last) >= k.ttl {
			delete(k.entries, key)
		}
	}
	k.nextSweep = now.Add(k.ttl / 2)
}

// Middleware throttles requests with l, keyed by keyFn(r). Rejected requests
// get 429 {"error":"rate_limited"} and a Retry-After header in whole seconds
// (rounded up, at least 1).
func Middleware(l *Keyed, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, retry := l.Allow(keyFn(r)); !ok {
				Reject(w, retry)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Reject answers 429 {"error":"rate_limited"} with a Retry-After header.
func Reject(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(retryAfter)))
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate_limited"}` + "\n"))
}

// RetryAfterSeconds converts a wait into a Retry-After value: whole seconds,
// rounded up, at least 1.
func RetryAfterSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}
