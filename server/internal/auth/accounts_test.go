package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// fakeDirectory is an AccountDirectory over a set of existing ids.
type fakeDirectory struct {
	mu    sync.Mutex
	users map[string]bool
	err   error
	calls atomic.Int32
	// hold, when set, is called after the lookup read its answer and before it
	// returns it: the window a concurrent deletion can slip into.
	hold func()
}

func newFakeDirectory(ids ...string) *fakeDirectory {
	d := &fakeDirectory{users: map[string]bool{}}
	for _, id := range ids {
		d.users[id] = true
	}
	return d
}

func (d *fakeDirectory) GetUser(_ context.Context, id string) (store.User, error) {
	d.calls.Add(1)
	d.mu.Lock()
	exists, err := d.users[id], d.err
	hold := d.hold
	d.mu.Unlock()
	if hold != nil {
		hold()
	}
	switch {
	case err != nil:
		return store.User{}, err
	case !exists:
		return store.User{}, store.ErrUserNotFound
	}
	return store.User{ID: id}, nil
}

func (d *fakeDirectory) add(id string) {
	d.mu.Lock()
	d.users[id] = true
	d.mu.Unlock()
}

func (d *fakeDirectory) remove(id string) {
	d.mu.Lock()
	delete(d.users, id)
	d.mu.Unlock()
}

func (d *fakeDirectory) setErr(err error) {
	d.mu.Lock()
	d.err = err
	d.mu.Unlock()
}

// checkedSessions is Sessions with an account check and a clock the test moves.
func checkedSessions(dir AccountDirectory, now *atomic.Pointer[time.Time]) *Sessions {
	return NewSessions([]byte(testSecret), false, WithAccounts(dir), WithSessionClock(func() time.Time { return *now.Load() }))
}

func clockAt(t time.Time) *atomic.Pointer[time.Time] {
	var p atomic.Pointer[time.Time]
	p.Store(&t)
	return &p
}

func TestLookupWithoutAccountCheckOnlyReadsTheCookie(t *testing.T) {
	s := sessionAt(testNow, false)
	c, id := issueSession(t, s)
	if got, st := s.Lookup(sessionReq(c.Value)); got != id || st != SessionActive {
		t.Errorf("Lookup = %q, %v; want %q, active", got, st, id)
	}
	if got, st := s.Lookup(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" || st != SessionNone {
		t.Errorf("Lookup without a cookie = %q, %v", got, st)
	}
}

// TestLookupStatuses: a cookie of a deleted account is not an identity, and a
// store that cannot answer is neither "deleted" (the cookie is kept) nor
// "active" (fail closed).
func TestLookupStatuses(t *testing.T) {
	clk := clockAt(testNow)
	dir := newFakeDirectory()
	s := checkedSessions(dir, clk)
	c, id := issueSession(t, s)
	req := func() *http.Request { return sessionReq(c.Value) }

	if got, st := s.Lookup(req()); got != "" || st != SessionGone {
		t.Errorf("account never created: Lookup = %q, %v; want gone", got, st)
	}
	if _, ok := s.Identify(req()); ok {
		t.Error("Identify accepted the cookie of an account that does not exist")
	}

	// A fresh account, looked up after the "gone" answer expired.
	dir.add(id)
	clk.Store(ptr(testNow.Add(accountGoneTTL + time.Second)))
	// The cookie itself is only valid for 30 days: still fine.
	if got, st := s.Lookup(req()); got != id || st != SessionActive {
		t.Errorf("Lookup = %q, %v; want %q, active", got, st, id)
	}

	// The store fails: unknown, and nothing is cached about it.
	clk.Store(ptr(testNow.Add(accountGoneTTL + 2*accountAliveTTL)))
	dir.setErr(errors.New("database down"))
	if got, st := s.Lookup(req()); got != "" || st != SessionUnknown {
		t.Errorf("store down: Lookup = %q, %v; want unknown", got, st)
	}
	if _, ok := s.Identify(req()); ok {
		t.Error("Identify accepted a session whose account could not be looked up")
	}
	dir.setErr(nil)
	if got, st := s.Lookup(req()); got != id || st != SessionActive {
		t.Errorf("store back: Lookup = %q, %v", got, st)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAccountCacheAnswersFromMemoryWithinTheTTL(t *testing.T) {
	clk := clockAt(testNow)
	dir := newFakeDirectory()
	s := checkedSessions(dir, clk)
	c, id := issueSession(t, s)
	dir.add(id)

	for range 5 {
		if _, ok := s.Identify(sessionReq(c.Value)); !ok {
			t.Fatal("not identified")
		}
	}
	if n := dir.calls.Load(); n != 1 {
		t.Errorf("%d store lookups for 5 requests, want 1", n)
	}

	// Past the TTL it asks again, and a deletion by somebody else is noticed.
	dir.remove(id)
	clk.Store(ptr(testNow.Add(accountAliveTTL - time.Second)))
	if _, ok := s.Identify(sessionReq(c.Value)); !ok {
		t.Error("the cached answer should hold until the TTL")
	}
	clk.Store(ptr(testNow.Add(accountAliveTTL + time.Second)))
	if _, ok := s.Identify(sessionReq(c.Value)); ok {
		t.Error("a deletion made behind the process's back was never noticed")
	}
	// The negative answer is cached too: a replayed cookie costs no query.
	before := dir.calls.Load()
	for range 5 {
		s.Identify(sessionReq(c.Value))
	}
	if dir.calls.Load() != before {
		t.Errorf("a replayed cookie of a deleted account hit the store %d times", dir.calls.Load()-before)
	}
}

// TestForgetTakesEffectAtOnce: deleting through this process does not wait for
// the cache to expire.
func TestForgetTakesEffectAtOnce(t *testing.T) {
	dir := newFakeDirectory()
	s := checkedSessions(dir, clockAt(testNow))
	c, id := issueSession(t, s)
	dir.add(id)
	if _, ok := s.Identify(sessionReq(c.Value)); !ok {
		t.Fatal("not identified before the deletion")
	}

	dir.remove(id)
	s.Forget(id)
	if got, st := s.Lookup(sessionReq(c.Value)); got != "" || st != SessionGone {
		t.Errorf("after Forget: Lookup = %q, %v; want gone", got, st)
	}
}

// TestForgetBeatsALookupInFlight: a lookup that read the account just before it
// was deleted must not put it back in the cache.
func TestForgetBeatsALookupInFlight(t *testing.T) {
	dir := newFakeDirectory()
	s := checkedSessions(dir, clockAt(testNow))
	c, id := issueSession(t, s)
	dir.add(id)

	dir.hold = func() { // the store has answered "exists"; now the account is deleted
		dir.remove(id)
		s.Forget(id)
	}
	if got, st := s.Lookup(sessionReq(c.Value)); got != "" || st != SessionGone {
		t.Fatalf("Lookup racing a deletion = %q, %v; want gone", got, st)
	}
	dir.hold = nil
	if _, ok := s.Identify(sessionReq(c.Value)); ok {
		t.Error("the racing lookup left the account in the cache")
	}
}

// TestLookupTwoCookies: when a request carries two session cookies, one active
// account is enough.
func TestLookupTwoCookies(t *testing.T) {
	dir := newFakeDirectory()
	s := checkedSessions(dir, clockAt(testNow))
	deadC, _ := issueSession(t, s)
	liveC, liveID := issueSession(t, s)
	dir.add(liveID)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("Cookie", SessionCookie+"="+deadC.Value)
	r.Header.Add("Cookie", SessionCookie+"="+liveC.Value)
	if got, st := s.Lookup(r); got != liveID || st != SessionActive {
		t.Errorf("Lookup = %q, %v; want %q, active", got, st, liveID)
	}
}

func TestUseAccountsSetsOnce(t *testing.T) {
	s := sessionAt(testNow, false)
	first, second := newFakeDirectory(), newFakeDirectory()
	if s.UseAccounts(nil) {
		t.Error("a nil directory turned the check on")
	}
	if !s.UseAccounts(first) {
		t.Fatal("UseAccounts refused the first directory")
	}
	if s.UseAccounts(second) {
		t.Error("UseAccounts replaced a directory that was already set")
	}
	c, id := issueSession(t, s)
	first.add(id)
	if _, ok := s.Identify(sessionReq(c.Value)); !ok {
		t.Error("the first directory is not in use")
	}
}

func TestAccountCacheIsBounded(t *testing.T) {
	m := map[string]time.Time{}
	for i := range maxCachedAccounts + 10 {
		remember(m, store.NewID(), testNow.Add(time.Hour), testNow)
		if len(m) > maxCachedAccounts {
			t.Fatalf("the cache grew to %d entries after %d inserts", len(m), i+1)
		}
	}
}

func TestAccountCacheConcurrent(t *testing.T) {
	dir := newFakeDirectory()
	s := checkedSessions(dir, clockAt(testNow))
	var wg sync.WaitGroup
	for range 8 {
		c, id := issueSession(t, s)
		dir.add(id)
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 50 {
				s.Identify(sessionReq(c.Value))
			}
		}()
		go func() {
			defer wg.Done()
			s.Forget(id)
		}()
	}
	wg.Wait()
}
