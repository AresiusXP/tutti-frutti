package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

const sessionSecret = "an-account-test-secret-long-enough-0123456789"

// sessionCookie issues a session cookie for user at now.
func sessionCookie(t *testing.T, now time.Time, user string) *http.Cookie {
	t.Helper()
	s := auth.NewSessions([]byte(sessionSecret), false, auth.WithSessionClock(func() time.Time { return now }))
	rec := httptest.NewRecorder()
	if err := s.Issue(rec, user); err != nil {
		t.Fatal(err)
	}
	return rec.Result().Cookies()[0]
}

// both makes one Cookie header out of the guest cookie and extra cookies.
func both(e *env, extra ...*http.Cookie) dialOpt {
	return func(h http.Header) {
		parts := []string{(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String()}
		for _, c := range extra {
			parts = append(parts, (&http.Cookie{Name: c.Name, Value: c.Value}).String())
		}
		h.Set("Cookie", strings.Join(parts, "; "))
	}
}

func withSessions(now time.Time) func(*Deps) {
	return func(d *Deps) {
		d.Sessions = auth.NewSessions([]byte(sessionSecret), false, auth.WithSessionClock(func() time.Time { return now }))
	}
}

// linkedUser dials with opts and returns what the room layer was told about
// the connection's account.
func linkedUser(t *testing.T, e *env, opts ...dialOpt) string {
	t.Helper()
	n := len(e.rooms.connectCalls())
	c := e.dial(opts...)
	send(t, c, "join", `{"code":"KT7P","name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}`)
	sess := e.rooms.session(t, n)
	linked, ok := sess.client.(room.UserLinked)
	if !ok {
		t.Fatal("the connection does not implement room.UserLinked")
	}
	if sess.client.PlayerID() != e.id {
		t.Errorf("player id = %q, want the guest id %q: logging in must not change it", sess.client.PlayerID(), e.id)
	}
	return linked.UserID()
}

func TestSessionCookieLinksTheConnectionToTheUser(t *testing.T) {
	now := time.Now()
	user := store.NewID()
	e := newEnv(t, withSessions(now))
	if got := linkedUser(t, e, both(e, sessionCookie(t, now, user))); got != user {
		t.Errorf("UserID = %q, want %q", got, user)
	}
}

func TestAnInvalidSessionIsJustAGuest(t *testing.T) {
	now := time.Now()
	user := store.NewID()
	good := sessionCookie(t, now, user)

	other := auth.NewSessions([]byte("another-secret-long-enough-for-the-test-xx"), false, auth.WithSessionClock(func() time.Time { return now }))
	rec := httptest.NewRecorder()
	_ = other.Issue(rec, user)
	foreign := rec.Result().Cookies()[0]

	tests := map[string]*http.Cookie{
		"no session":      nil,
		"garbage":         {Name: auth.SessionCookie, Value: "garbage"},
		"empty":           {Name: auth.SessionCookie, Value: ""},
		"another secret":  foreign,
		"truncated":       {Name: auth.SessionCookie, Value: good.Value[:len(good.Value)-1]},
		"a changed user":  {Name: auth.SessionCookie, Value: store.NewID() + good.Value[36:]},
		"expired":         sessionCookie(t, now.Add(-31*24*time.Hour), user),
		"oversized value": {Name: auth.SessionCookie, Value: good.Value + strings.Repeat("A", 4000)},
	}
	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withSessions(now))
			var opts []dialOpt
			if c != nil {
				opts = append(opts, both(e, c))
			}
			if got := linkedUser(t, e, opts...); got != "" {
				t.Errorf("UserID = %q, want a guest", got)
			}
		})
	}
	t.Run("the guest cookie presented as a session", func(t *testing.T) {
		e := newEnv(t, withSessions(now))
		if got := linkedUser(t, e, both(e, &http.Cookie{Name: auth.SessionCookie, Value: e.cookie.Value})); got != "" {
			t.Errorf("UserID = %q, want a guest", got)
		}
	})
}

func TestWithoutSessionsEverybodyIsAGuest(t *testing.T) {
	now := time.Now()
	e := newEnv(t, nil)
	if got := linkedUser(t, e, both(e, sessionCookie(t, now, store.NewID()))); got != "" {
		t.Errorf("UserID = %q, want a guest when the handler has no Sessions", got)
	}
}

func TestSessionAloneDoesNotAuthenticateTheSocket(t *testing.T) {
	// The guest cookie is still what identifies the player: a session without
	// it is refused like any request without a guest cookie.
	now := time.Now()
	e := newEnv(t, withSessions(now))
	_, resp, err := e.tryDial(func(h http.Header) {
		h.Set("Cookie", (&http.Cookie{Name: auth.SessionCookie, Value: sessionCookie(t, now, store.NewID()).Value}).String())
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial with only a session = %v (%v), want 401", resp, err)
	}
}
