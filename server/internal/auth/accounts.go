package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Tunables of the account check of Sessions.
const (
	// accountAliveTTL is how long "this account exists" is remembered. It is the
	// longest a copied session cookie can keep working after the account was
	// deleted by something that is not this process (a database edit, the
	// moderation tool); a deletion through this process takes effect at once
	// (see Sessions.Forget).
	accountAliveTTL = 30 * time.Second
	// accountGoneTTL is how long "this account is gone" is remembered: it saves
	// the store a query per request of a replayed cookie. Ids are random UUIDs and
	// are never reused, so a stale answer is never wrong, only unnecessary; the
	// bound is memory, and the cookie's own 30 days are not remembered.
	accountGoneTTL = 10 * time.Minute
	// accountLookupTimeout bounds one store lookup.
	accountLookupTimeout = 3 * time.Second
	// maxCachedAccounts bounds each of the two caches.
	maxCachedAccounts = 4096
)

// AccountDirectory is what Sessions needs to know whether the account behind a
// session still exists. store.Accounts implements it.
type AccountDirectory interface {
	// GetUser returns the user, or an error wrapping store.ErrUserNotFound.
	GetUser(ctx context.Context, id string) (store.User, error)
}

// SessionStatus is the outcome of Sessions.Lookup.
type SessionStatus int

const (
	// SessionNone: the request carries no valid session cookie (a guest).
	SessionNone SessionStatus = iota
	// SessionActive: a valid cookie whose account exists (or no account check is
	// configured, see WithAccounts).
	SessionActive
	// SessionGone: a valid cookie of an account that was deleted: a copy of a
	// cookie that outlived its account. The account endpoints answer 401 and
	// clear it; the WebSocket treats the player as a guest.
	SessionGone
	// SessionUnknown: a valid cookie whose account the store could not look up.
	// Callers fail closed (no account capability) but must not clear the cookie:
	// the account may well exist.
	SessionUnknown
)

// WithAccounts makes the sessions verify that the account behind a valid cookie
// still exists (see Sessions.Lookup). Without it a session is only a signed
// cookie, which cannot be revoked.
func WithAccounts(dir AccountDirectory) SessionOption {
	return func(s *Sessions) { s.useAccounts(dir) }
}

// UseAccounts is WithAccounts for Sessions that already exist: it turns the
// account check on unless one is already set, and reports whether it did. It
// must be called before the Sessions serve requests (httpapi.New does when it
// is given both Sessions and Accounts, so a server that forgot to wire it still
// checks).
func (s *Sessions) UseAccounts(dir AccountDirectory) bool { return s.useAccounts(dir) }

func (s *Sessions) useAccounts(dir AccountDirectory) bool {
	if dir == nil {
		return false
	}
	return s.accounts.CompareAndSwap(nil, &accountCache{
		dir:   dir,
		alive: map[string]time.Time{},
		gone:  map[string]time.Time{},
	})
}

// Lookup is Identify with the status of the account: the user id of a valid,
// unexpired session cookie of r whose account exists. When no account check is
// configured it only reads the cookie. The id is returned only for
// SessionActive.
//
// The existence check is cached in the process for 30 s (a positive answer) and
// for 10 min (a negative one); Forget drops both at once, which is how a
// deletion through this process takes effect immediately. A restart flushes the
// cache (and with the single replica, the Recreate strategy, there is no other
// process to disagree).
func (s *Sessions) Lookup(r *http.Request) (string, SessionStatus) {
	var ids []string
	for _, c := range r.CookiesNamed(SessionCookie) {
		if id, ok := s.verify(c.Value); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", SessionNone
	}
	cache := s.accounts.Load()
	if cache == nil {
		return ids[0], SessionActive
	}
	status := SessionGone
	for _, id := range ids {
		switch cache.check(r.Context(), id, s.now) {
		case accountAlive:
			return id, SessionActive
		case accountUnknown:
			status = SessionUnknown
		}
	}
	return "", status
}

// Forget records that the account was deleted: from now on Lookup reports its
// cookies as SessionGone without asking the store, and a lookup that was
// already in flight (it may have read the account just before the deletion)
// cannot bring it back. Call it after the store deleted the account.
func (s *Sessions) Forget(userID string) {
	if cache := s.accounts.Load(); cache != nil {
		cache.forget(userID, s.now())
	}
}

type accountStatus int

const (
	accountAlive accountStatus = iota
	accountDeleted
	accountUnknown
)

// accountCache remembers which accounts exist and which are gone.
type accountCache struct {
	dir AccountDirectory

	mu    sync.Mutex
	alive map[string]time.Time // id -> until when "exists" may be trusted
	gone  map[string]time.Time // id -> until when "deleted" is remembered
}

func (c *accountCache) check(ctx context.Context, id string, clock func() time.Time) accountStatus {
	c.mu.Lock()
	now := clock()
	if until, ok := c.gone[id]; ok && now.Before(until) {
		c.mu.Unlock()
		return accountDeleted
	}
	if until, ok := c.alive[id]; ok && now.Before(until) {
		c.mu.Unlock()
		return accountAlive
	}
	c.mu.Unlock()

	lookupCtx, cancel := context.WithTimeout(ctx, accountLookupTimeout)
	_, err := c.dir.GetUser(lookupCtx, id)
	cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	now = clock()
	// Forgotten while the store was being asked: the answer may predate the
	// deletion, so it must not be trusted.
	if until, ok := c.gone[id]; ok && now.Before(until) {
		return accountDeleted
	}
	switch {
	case err == nil:
		remember(c.alive, id, now.Add(accountAliveTTL), now)
		return accountAlive
	case errors.Is(err, store.ErrUserNotFound):
		remember(c.gone, id, now.Add(accountGoneTTL), now)
		return accountDeleted
	}
	return accountUnknown
}

func (c *accountCache) forget(id string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.alive, id)
	remember(c.gone, id, now.Add(accountGoneTTL), now)
}

// remember stores until under id, keeping the map within maxCachedAccounts:
// when it is full the expired entries go, and if that is not enough everything
// does (the cache is only an optimisation, and losing it costs a query).
func remember(m map[string]time.Time, id string, until, now time.Time) {
	if len(m) >= maxCachedAccounts {
		for k, u := range m {
			if !now.Before(u) {
				delete(m, k)
			}
		}
		if len(m) >= maxCachedAccounts {
			clear(m)
		}
	}
	m[id] = until
}
