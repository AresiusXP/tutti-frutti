package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// directory is the accounts store of these tests: the ids that exist.
type directory struct {
	mu  sync.Mutex
	ids map[string]bool
	err error
}

func (d *directory) GetUser(_ context.Context, id string) (store.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.err != nil:
		return store.User{}, d.err
	case !d.ids[id]:
		return store.User{}, store.ErrUserNotFound
	}
	return store.User{ID: id}, nil
}

func (d *directory) set(id string, exists bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids[id] = exists
}

// withAccounts makes the handler's sessions check the accounts of dir.
func withAccounts(now time.Time, dir *directory) func(*Deps) {
	return func(d *Deps) {
		d.Sessions = auth.NewSessions([]byte(sessionSecret), false, auth.WithAccounts(dir), auth.WithSessionClock(func() time.Time { return now }))
	}
}

// TestACopiedCookieOfADeletedAccountIsAGuestOnTheWebSocket: a signed cookie that
// outlived its account does not link the connection to anybody (statistics and
// the AI judge depend on that link).
func TestACopiedCookieOfADeletedAccountIsAGuestOnTheWebSocket(t *testing.T) {
	now := time.Now()
	user := store.NewID()
	dir := &directory{ids: map[string]bool{user: true}}
	cookie := sessionCookie(t, now, user)

	t.Run("the account exists", func(t *testing.T) {
		e := newEnv(t, withAccounts(now, dir))
		if got := linkedUser(t, e, both(e, cookie)); got != user {
			t.Errorf("UserID = %q, want %q", got, user)
		}
	})
	t.Run("the account was deleted", func(t *testing.T) {
		dir.set(user, false)
		e := newEnv(t, withAccounts(now, dir))
		if got := linkedUser(t, e, both(e, cookie)); got != "" {
			t.Errorf("UserID = %q after the deletion, want a guest", got)
		}
	})
}

// TestSessionsForgetTakesEffectOnTheNextUpgrade: the cache of the sessions does
// not keep a deleted account alive on this process.
func TestSessionsForgetTakesEffectOnTheNextUpgrade(t *testing.T) {
	now := time.Now()
	user := store.NewID()
	dir := &directory{ids: map[string]bool{user: true}}
	cookie := sessionCookie(t, now, user)

	e := newEnv(t, withAccounts(now, dir))
	if got := linkedUser(t, e, both(e, cookie)); got != user {
		t.Fatalf("UserID = %q before the deletion", got)
	}
	dir.set(user, false)
	e.h.sessions.Forget(user)
	if got := linkedUser(t, e, both(e, cookie)); got != "" {
		t.Errorf("UserID = %q right after Forget, want a guest", got)
	}
}

// TestAStoreThatCannotAnswerMakesAGuestNotAnError: the socket still opens (the
// guest cookie is what identifies the player), without the account.
func TestAStoreThatCannotAnswerMakesAGuestNotAnError(t *testing.T) {
	now := time.Now()
	user := store.NewID()
	dir := &directory{ids: map[string]bool{user: true}, err: errors.New("database down")}
	e := newEnv(t, withAccounts(now, dir))
	if got := linkedUser(t, e, both(e, sessionCookie(t, now, user))); got != "" {
		t.Errorf("UserID = %q, want a guest while the store is down", got)
	}
	// And a plain upgrade is unaffected.
	if _, resp, err := e.tryDial(both(e)); err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("dial = %v, %v", resp, err)
	}
}
