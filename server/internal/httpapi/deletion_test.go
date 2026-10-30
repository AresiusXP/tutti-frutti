package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// forgetRooms is the fake room hub of a server that is told about deleted
// accounts (room.AccountForgetter).
type forgetRooms struct {
	*fakeRooms
	mu        sync.Mutex
	forgotten []string
	// onForget runs inside ForgetAccount: what the hub sees when it is told.
	onForget func(ctx context.Context, userID string) error
}

func (f *forgetRooms) ForgetAccount(ctx context.Context, userID string) error {
	f.mu.Lock()
	f.forgotten = append(f.forgotten, userID)
	fn := f.onForget
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, userID)
	}
	return nil
}

func (f *forgetRooms) told() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forgotten...)
}

var _ room.AccountForgetter = (*forgetRooms)(nil)

// sessionCookieOf returns the Cookie header value of the browser's session.
func sessionCookieOf(t *testing.T, b *browser) string {
	t.Helper()
	c := b.cookie("tf_session")
	if c == nil {
		t.Fatal("the browser holds no session cookie")
	}
	return (&http.Cookie{Name: c.Name, Value: c.Value}).String()
}

// TestDeleteAccountOrder: the store first, then the sessions, then the rooms,
// and only then the response that clears the cookie.
func TestDeleteAccountOrder(t *testing.T) {
	rooms := &forgetRooms{fakeRooms: &fakeRooms{}}
	var e *acctEnv
	var cookie string
	type seen struct {
		inStore bool
		session auth.SessionStatus
	}
	var atForget seen
	e = newAcctEnv(t, acctOpts{tweak: func(o *Options) { o.Rooms = rooms }})
	b := e.browser()
	b.login("/", testUser)
	me, _ := b.me()
	cookie = sessionCookieOf(t, b)

	rooms.onForget = func(ctx context.Context, userID string) error {
		_, err := e.mem.GetUser(ctx, userID)
		atForget.inStore = err == nil
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Cookie", cookie)
		_, atForget.session = e.sessions.Lookup(req)
		return nil
	}

	r := b.request(http.MethodDelete, "/api/me", "")
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", r.StatusCode, r.body)
	}
	if got := rooms.told(); len(got) != 1 || got[0] != me.ID {
		t.Fatalf("the rooms were told %v, want [%s]", got, me.ID)
	}
	if atForget.inStore {
		t.Error("the rooms were told while the account was still in the store")
	}
	if atForget.session != auth.SessionGone {
		t.Errorf("the rooms were told while the sessions still knew the account (%v)", atForget.session)
	}
	if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "tf_session=") || !strings.Contains(h, "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want the session cookie expired", h)
	}
}

// TestDeleteAccountWhenTheRoomsCannotConfirm: the account is gone either way;
// the answer is still 204 (the cookie is cleared, nothing is left to retry) and
// the failure is logged.
func TestDeleteAccountWhenTheRoomsCannotConfirm(t *testing.T) {
	rooms := &forgetRooms{fakeRooms: &fakeRooms{}, onForget: func(context.Context, string) error { return context.DeadlineExceeded }}
	e := newAcctEnv(t, acctOpts{tweak: func(o *Options) { o.Rooms = rooms }})
	b := e.browser()
	b.login("/", testUser)
	me, _ := b.me()
	if r := b.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", r.StatusCode, r.body)
	}
	if _, err := e.mem.GetUser(context.Background(), me.ID); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("the account survived: %v", err)
	}
	if !strings.Contains(e.logs.String(), "could not confirm they forgot a deleted account") {
		t.Errorf("the failure was not logged:\n%s", e.logs.String())
	}
}

func TestAFailedDeletionDoesNotTouchTheRoomsOrTheSessions(t *testing.T) {
	rooms := &forgetRooms{fakeRooms: &fakeRooms{}}
	e := newAcctEnv(t, acctOpts{tweak: func(o *Options) { o.Rooms = rooms }})
	b := e.browser()
	b.login("/", testUser)
	e.accounts.set("delete", errors.New("boom"))
	if r := b.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusInternalServerError {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}
	if got := rooms.told(); len(got) != 0 {
		t.Errorf("the rooms were told about an account that still exists: %v", got)
	}
	if _, status := b.me(); status != http.StatusOK {
		t.Errorf("the account stopped working after a failed deletion: %d", status)
	}
}

// TestACopiedCookieAfterTheDeletion: the cookie is signed for 30 days, so a copy
// of it (another device, a stolen one) is only refused because the account is
// gone, on every endpoint that reads it.
func TestACopiedCookieAfterTheDeletion(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	b.get("/api/session")
	cookie := sessionCookieOf(t, b)
	copied := cookie + "; " + (&http.Cookie{Name: "tf_guest", Value: b.cookie("tf_guest").Value}).String()
	if r := e.rawRequest(t, http.MethodGet, "/api/me", "", copied); r.StatusCode != http.StatusOK {
		t.Fatalf("setup: the copy does not work before the deletion: %d", r.StatusCode)
	}

	if r := b.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/me", ""},
		{http.MethodGet, "/api/me/stats", ""},
		{http.MethodPut, "/api/me/profile", validProfile},
		{http.MethodDelete, "/api/me", ""},
	} {
		r := e.rawRequest(t, tc.method, tc.path, tc.body, copied)
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with a copied cookie = %d %s", tc.method, tc.path, r.StatusCode, r.body)
		}
		if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "tf_session=") || !strings.Contains(h, "Max-Age=0") {
			t.Errorf("%s %s: the copy was not expired (%q)", tc.method, tc.path, h)
		}
	}

	// The session endpoint says it is a guest and expires the copy too.
	r := e.rawRequest(t, http.MethodGet, "/api/session", "", copied)
	var info api.SessionInfo
	r.json(t, &info)
	if info.LoggedIn || !info.Guest {
		t.Errorf("session = %+v: a deleted account's cookie is a guest", info)
	}
	if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "tf_session=") || !strings.Contains(h, "Max-Age=0") {
		t.Errorf("GET /api/session did not expire the copy (%q)", h)
	}
}

// TestLoginAfterTheDeletionIsANewAccount: the Google account maps to a new id
// and the old cookie stays refused (it names the old id).
func TestLoginAfterTheDeletionIsANewAccount(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	old, _ := b.me()
	b.request(http.MethodPut, "/api/me/profile", validProfile)
	oldCookie := sessionCookieOf(t, b)
	if r := b.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}

	b2 := e.browser()
	b2.login("/", testUser)
	again, status := b2.me()
	if status != http.StatusOK || again.ID == old.ID || again.Profile != nil {
		t.Fatalf("the new account = %+v (%d): it must be new and empty", again, status)
	}
	var info api.SessionInfo
	b2.get("/api/session").json(t, &info)
	if !info.LoggedIn {
		t.Error("the new account is not logged in")
	}
	if r := e.rawRequest(t, http.MethodGet, "/api/me", "", oldCookie); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old cookie works again after the Google account signed in anew: %d", r.StatusCode)
	}
}

// TestADeletionBehindTheServersBack: an account removed by something that is not
// this process (a database edit) is noticed by every endpoint that asks the
// store at once, and by the ones that only read the session when the cache
// entry expires (30 s).
func TestADeletionBehindTheServersBack(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	me, _ := b.me() // the account is now in the cache
	cookie := sessionCookieOf(t, b)
	if err := e.mem.DeleteUser(context.Background(), me.ID); err != nil {
		t.Fatal(err)
	}

	if r := e.rawRequest(t, http.MethodGet, "/api/me", "", cookie); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/me = %d: the endpoints read the store themselves", r.StatusCode)
	}
	var info api.SessionInfo
	e.clock.Advance(31 * time.Second)
	e.rawRequest(t, http.MethodGet, "/api/session", "", cookie).json(t, &info)
	if info.LoggedIn {
		t.Error("loggedIn is still true 31 s after the account was deleted")
	}
}

// TestAStoreThatCannotAnswerIsNotADeletion: the cookie is kept (the account may
// well exist) and the request is a server error, not a logout.
func TestAStoreThatCannotAnswerIsNotADeletion(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	cookie := sessionCookieOf(t, b)
	e.clock.Advance(time.Minute) // the cache of the login is cold

	e.accounts.set("get", errors.New("database down"))
	r := e.rawRequest(t, http.MethodGet, "/api/me", "", cookie)
	if r.StatusCode != http.StatusInternalServerError || r.errCode(t) != "internal" {
		t.Errorf("GET /api/me = %d %s", r.StatusCode, r.body)
	}
	if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); strings.Contains(h, "tf_session=") {
		t.Errorf("the session was cleared although the store could not tell: %q", h)
	}
	var info api.SessionInfo
	r = e.rawRequest(t, http.MethodGet, "/api/session", "", cookie)
	r.json(t, &info)
	if info.LoggedIn {
		t.Error("loggedIn although the account could not be checked")
	}
	if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); strings.Contains(h, "tf_session=") {
		t.Errorf("GET /api/session cleared the session although the store could not tell: %q", h)
	}

	e.accounts.set("get", nil)
	if r := e.rawRequest(t, http.MethodGet, "/api/me", "", cookie); r.StatusCode != http.StatusOK {
		t.Errorf("the account does not work once the store is back: %d", r.StatusCode)
	}
}

// TestSessionOfAnAccountThatNeverExisted: a validly signed cookie is not enough
// (a database restored from an older backup, a copied secret).
func TestSessionOfAnAccountThatNeverExisted(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	rec := httptest.NewRecorder()
	if err := e.sessions.Issue(rec, store.NewID()); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0].String()
	var info api.SessionInfo
	e.rawRequest(t, http.MethodGet, "/api/session", "", cookie).json(t, &info)
	if info.LoggedIn {
		t.Error("a session of an account that does not exist is logged in")
	}
}

// TestReportsFromADeletedAccountsCookieAreGuestReports: the reporter's account
// comes from the session, which must not name a deleted account.
func TestReportsFromADeletedAccountsCookieAreGuestReports(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	b := e.browser()
	b.get("/api/session")
	b.acceptTerms()
	b.login("/", testUser)
	cookie := sessionCookieOf(t, b)
	guest := (&http.Cookie{Name: "tf_guest", Value: b.cookie("tf_guest").Value}).String()
	if r := b.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}

	r := e.rawRequest(t, http.MethodPost, "/api/reports", reportBody(nil), cookie+"; "+guest)
	var resp api.ReportResponse
	r.json(t, &resp)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("report = %d %s", r.StatusCode, r.body)
	}
	rep, err := e.safe.Memory.GetReport(context.Background(), resp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ReporterUser != "" || rep.ReporterGuest != b.guest() {
		t.Errorf("reporter = guest %q user %q: a deleted account is not an identity", rep.ReporterGuest, rep.ReporterUser)
	}
}
