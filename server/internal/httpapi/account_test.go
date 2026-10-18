package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

const (
	acctClientID     = "123-test.apps.googleusercontent.com"
	acctClientSecret = "fake-account-test-secret"
	acctSecret       = "an-account-test-secret-long-enough-0123456789"
	acctPublicURL    = "http://localhost:8000"
	acctOrigin       = acctPublicURL
)

// flakyAccounts wraps the memory store and fails the calls a test asks for.
type flakyAccounts struct {
	store.Accounts
	mu   sync.Mutex
	fail map[string]error
}

func (f *flakyAccounts) failing(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fail[op]
}

func (f *flakyAccounts) set(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail == nil {
		f.fail = map[string]error{}
	}
	f.fail[op] = err
}

func (f *flakyAccounts) UpsertGoogleUser(ctx context.Context, sub, name string) (store.User, error) {
	if err := f.failing("upsert"); err != nil {
		return store.User{}, err
	}
	return f.Accounts.UpsertGoogleUser(ctx, sub, name)
}

func (f *flakyAccounts) GetUser(ctx context.Context, id string) (store.User, error) {
	if err := f.failing("get"); err != nil {
		return store.User{}, err
	}
	return f.Accounts.GetUser(ctx, id)
}

func (f *flakyAccounts) SaveProfile(ctx context.Context, id string, p api.Profile) error {
	if err := f.failing("save"); err != nil {
		return err
	}
	return f.Accounts.SaveProfile(ctx, id, p)
}

func (f *flakyAccounts) DeleteUser(ctx context.Context, id string) error {
	if err := f.failing("delete"); err != nil {
		return err
	}
	return f.Accounts.DeleteUser(ctx, id)
}

func (f *flakyAccounts) UserStats(ctx context.Context, id string) (api.Stats, error) {
	if err := f.failing("stats"); err != nil {
		return api.Stats{}, err
	}
	return f.Accounts.UserStats(ctx, id)
}

type acctEnv struct {
	t        *testing.T
	idp      *authtest.IdP
	accounts *flakyAccounts
	mem      *store.Memory
	sessions *auth.Sessions
	google   *auth.Google
	srv      *Server
	ts       *httptest.Server
	clock    *clock
	logs     *syncLog
}

type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncLog) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncLog) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type acctOpts struct {
	noGoogle   bool
	noAccounts bool
	secure     bool
	limits     Limits
	tweak      func(*Options)
}

func newAcctEnv(t *testing.T, o acctOpts) *acctEnv {
	t.Helper()
	logs := &syncLog{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clk := &clock{t: time.Now()}
	idp := authtest.New(t, acctClientID, acctClientSecret)
	idp.SetClock(clk.Now)
	mem := store.NewMemory()
	accounts := &flakyAccounts{Accounts: mem}
	sessions := auth.NewSessions([]byte(acctSecret), o.secure, auth.WithSessionClock(clk.Now))

	opts := Options{
		Logger: logger, Version: "test", Rooms: &fakeRooms{}, Guests: auth.NewGuests([]byte(acctSecret), o.secure, logger),
		Sessions: sessions, AllowedOrigins: []string{acctOrigin}, Now: clk.Now, Limits: o.limits,
	}
	if !o.noAccounts {
		opts.Accounts = accounts
	}
	var google *auth.Google
	if !o.noGoogle {
		google = auth.NewGoogle(auth.GoogleConfig{
			ClientID: acctClientID, ClientSecret: acctClientSecret, RedirectURL: acctPublicURL + auth.GoogleCallbackPath,
			Secret: []byte(acctSecret), Secure: o.secure, Issuer: idp.URL, Now: clk.Now, Logger: logger,
		})
		opts.Google = google
	}
	if o.tweak != nil {
		o.tweak(&opts)
	}
	srv := New(opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &acctEnv{t: t, idp: idp, accounts: accounts, mem: mem, sessions: sessions, google: google, srv: srv, ts: ts, clock: clk, logs: logs}
}

// browser is a cookie-keeping HTTP client that never follows redirects, like
// the tests of a login need.
type browser struct {
	t      *testing.T
	e      *acctEnv
	client *http.Client
}

func (e *acctEnv) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: e.t, e: e, client: &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type reply struct {
	*http.Response
	body []byte
}

func (r reply) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
}

func (r reply) errCode(t *testing.T) string {
	t.Helper()
	var e api.ErrorResponse
	r.json(t, &e)
	return e.Error
}

func (b *browser) request(method, target, body string, hdr ...string) reply {
	b.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, b.e.ts.URL+target, rd)
	if err != nil {
		b.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", acctOrigin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return reply{resp, data}
}

func (b *browser) get(target string) reply { return b.request(http.MethodGet, target, "") }

// cookie returns the named cookie the browser holds for the server.
func (b *browser) cookie(name string) *http.Cookie {
	u, _ := url.Parse(b.e.ts.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	// Path-scoped cookies (tf_oauth) are only visible for their path.
	u.Path = "/api/auth/google/login"
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// me is GET /api/me: the status and, on 200, the user.
func (b *browser) me() (api.UserInfo, int) {
	b.t.Helper()
	r := b.get("/api/me")
	var me api.UserInfo
	if r.StatusCode == http.StatusOK {
		r.json(b.t, &me)
	}
	return me, r.StatusCode
}

// loginStart is GET /api/auth/google/login?returnTo=...; it returns the reply
// (a redirect to the provider).
func (b *browser) loginStart(returnTo string) reply {
	q := ""
	if returnTo != "\x00" {
		q = "?returnTo=" + url.QueryEscape(returnTo)
	}
	return b.get("/api/auth/google/login" + q)
}

// callbackURL has the provider authorize user for the login the browser just
// started and returns the callback path the browser is sent back to.
func (b *browser) callbackURL(start reply, user authtest.User) string {
	b.t.Helper()
	loc := start.Header.Get("Location")
	cb, err := b.e.idp.Authorize(loc, user)
	if err != nil {
		b.t.Fatalf("provider: %v (Location %q)", err, loc)
	}
	u, err := url.Parse(cb)
	if err != nil {
		b.t.Fatal(err)
	}
	return u.RequestURI()
}

// login performs a whole successful login and returns the final Location.
func (b *browser) login(returnTo string, user authtest.User) string {
	b.t.Helper()
	start := b.loginStart(returnTo)
	if start.StatusCode != http.StatusFound {
		b.t.Fatalf("login start = %d %s", start.StatusCode, start.body)
	}
	cb := b.get(b.callbackURL(start, user))
	if cb.StatusCode != http.StatusFound {
		b.t.Fatalf("callback = %d %s", cb.StatusCode, cb.body)
	}
	return cb.Header.Get("Location")
}

var testUser = authtest.User{Sub: "110169484474386276334", Name: "Ana García"}

func (e *acctEnv) metric(result string) float64 {
	e.t.Helper()
	mfs, err := e.srv.Registry().Gather()
	if err != nil {
		e.t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "tuttifrutti_logins_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if len(m.GetLabel()) == 1 && m.GetLabel()[0].GetValue() == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	e.t.Fatalf("no tuttifrutti_logins_total{result=%q}", result)
	return 0
}

func TestSessionEndpointReportsTheAccountState(t *testing.T) {
	t.Run("google enabled, nobody logged in", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		var got api.SessionInfo
		r := e.browser().get("/api/session")
		r.json(t, &got)
		if got != (api.SessionInfo{Guest: true, GoogleEnabled: true, LoggedIn: false}) {
			t.Errorf("session = %+v", got)
		}
	})
	t.Run("google disabled", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{noGoogle: true})
		var got api.SessionInfo
		e.browser().get("/api/session").json(t, &got)
		if got != (api.SessionInfo{Guest: true}) {
			t.Errorf("session = %+v", got)
		}
	})
	t.Run("logged in without a database lookup", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		var got api.SessionInfo
		b.get("/api/session").json(t, &got)
		if !got.LoggedIn {
			t.Fatalf("session = %+v, want loggedIn", got)
		}
		// Even a broken or empty database does not change loggedIn.
		e.accounts.set("get", errors.New("database down"))
		b.get("/api/session").json(t, &got)
		if !got.LoggedIn {
			t.Error("loggedIn depends on the database")
		}
		if me := b.get("/api/me"); me.StatusCode != http.StatusInternalServerError {
			t.Errorf("/api/me with the database down = %d", me.StatusCode)
		}
	})
	t.Run("expired and forged sessions are not logged in", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.get("/api/session")
		b.login("/", testUser)
		forged := strings.Repeat("a", 36) + ".9999999999." + strings.Repeat("A", 43)
		for name, hdr := range map[string]string{
			"forged":    "tf_session=" + forged,
			"empty":     "tf_session=",
			"a guest":   "tf_session=" + b.cookie("tf_guest").Value,
			"truncated": "tf_session=" + b.cookie("tf_session").Value[:50],
		} {
			var got api.SessionInfo
			e.rawGet(t, "/api/session", hdr).json(t, &got)
			if got.LoggedIn {
				t.Errorf("%s session counts as logged in", name)
			}
		}
		e.clock.Advance(31 * 24 * time.Hour)
		var got api.SessionInfo
		b.get("/api/session").json(t, &got)
		if got.LoggedIn {
			t.Error("a session older than 30 days is logged in")
		}
	})
	t.Run("without Sessions loggedIn is false", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{tweak: func(o *Options) { o.Sessions = nil; o.Accounts = nil; o.Google = nil }})
		var got api.SessionInfo
		e.browser().get("/api/session").json(t, &got)
		if got != (api.SessionInfo{Guest: true}) {
			t.Errorf("session = %+v", got)
		}
	})
}

// rawGet is a GET with a verbatim Cookie header and no jar.
func (e *acctEnv) rawGet(t *testing.T, target, cookie string) reply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+target, nil)
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return reply{resp, data}
}

func TestGoogleLoginRedirectsToTheProvider(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	r := b.loginStart("/kt7p")
	if r.StatusCode != http.StatusFound {
		t.Fatalf("status = %d %s", r.StatusCode, r.body)
	}
	loc, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != e.idp.URL+"/auth" {
		t.Errorf("redirected to %q", got)
	}
	q := loc.Query()
	for k, want := range map[string]string{
		"response_type": "code", "client_id": acctClientID, "redirect_uri": acctPublicURL + "/api/auth/google/callback",
		"scope": "openid profile", "code_challenge_method": "S256", "prompt": "select_account",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if len(q.Get(k)) < 20 {
			t.Errorf("%s = %q: too short", k, q.Get(k))
		}
	}
	if len(r.body) != 0 {
		t.Errorf("the redirect has a body: %q", r.body)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	oc := b.cookie("tf_oauth")
	if oc == nil {
		t.Fatal("no tf_oauth cookie")
	}
	// The raw Set-Cookie carries the attributes the jar hides.
	var set string
	for _, h := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(h, "tf_oauth=") {
			set = h
		}
	}
	for _, want := range []string{"Path=/api/auth/google", "Max-Age=600", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	if strings.Contains(set, "Secure") {
		t.Error("the cookie is Secure although PUBLIC_URL is http")
	}
}

func TestGoogleCookiesAreSecureOverHTTPS(t *testing.T) {
	e := newAcctEnv(t, acctOpts{secure: true})
	b := e.browser()
	r := b.loginStart("/")
	for _, h := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(h, "tf_oauth=") && !strings.Contains(h, "Secure") {
			t.Errorf("tf_oauth is not Secure: %s", h)
		}
	}
	// And the session cookie, issued by the callback, as well.
	e2 := newAcctEnv(t, acctOpts{secure: true})
	b2 := e2.browser()
	start := b2.loginStart("/")
	cb := b2.get(b2.callbackURL(start, testUser))
	found := false
	for _, h := range cb.Header.Values("Set-Cookie") {
		if strings.HasPrefix(h, "tf_session=") {
			found = true
			for _, want := range []string{"Secure", "HttpOnly", "SameSite=Lax", "Path=/", "Max-Age=2592000"} {
				if !strings.Contains(h, want) {
					t.Errorf("Set-Cookie %q lacks %q", h, want)
				}
			}
		}
	}
	if !found {
		t.Error("the callback did not set tf_session")
	}
}

func TestGoogleDisabled(t *testing.T) {
	e := newAcctEnv(t, acctOpts{noGoogle: true})
	b := e.browser()
	for _, target := range []string{"/api/auth/google/login", "/api/auth/google/login?returnTo=%2FKT7P", "/api/auth/google/callback?code=x&state=y"} {
		r := b.get(target)
		if r.StatusCode != http.StatusNotFound || r.errCode(t) != "auth_disabled" {
			t.Errorf("GET %s = %d %s, want 404 auth_disabled", target, r.StatusCode, r.body)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q", target, r.Header.Get("Cache-Control"))
		}
	}
	if b.cookie("tf_oauth") != nil {
		t.Error("a disabled login set a cookie")
	}
}

func TestGoogleLoginFlow(t *testing.T) {
	t.Run("success returns to the room and logs in", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		if loc := b.login("/kt7p", testUser); loc != "/KT7P" {
			t.Errorf("redirected to %q, want /KT7P", loc)
		}
		if b.cookie("tf_session") == nil {
			t.Fatal("no session cookie")
		}
		if b.cookie("tf_oauth") != nil {
			t.Error("the transaction cookie survived the callback")
		}

		var me api.UserInfo
		r := b.get("/api/me")
		r.json(t, &me)
		if r.StatusCode != http.StatusOK || me.Name != "Ana García" || me.Profile != nil || !store.ValidID(me.ID) {
			t.Fatalf("/api/me = %d %+v", r.StatusCode, me)
		}
		var session api.SessionInfo
		b.get("/api/session").json(t, &session)
		if !session.LoggedIn {
			t.Error("session says loggedIn=false after the login")
		}
		if got := e.metric("ok"); got != 1 {
			t.Errorf("logins_total{ok} = %v", got)
		}

		// The same Google account logging in again is the same user.
		b2 := e.browser()
		b2.login("/", testUser)
		var again api.UserInfo
		b2.get("/api/me").json(t, &again)
		if again.ID != me.ID {
			t.Errorf("second login got user %s, want %s", again.ID, me.ID)
		}
	})
	t.Run("returnTo outside the allow-list goes home", func(t *testing.T) {
		for _, rt := range []string{"", "//evil.com", `/\evil.com`, "https://evil.com", "/KT7P/../x", "/stats", "/KT7PX", "\x00"} {
			e := newAcctEnv(t, acctOpts{})
			if loc := e.browser().login(rt, testUser); loc != "/" {
				t.Errorf("returnTo %q -> Location %q, want /", rt, loc)
			}
		}
	})
	t.Run("the callback ignores a returnTo of its own", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		start := b.loginStart("/KT7P")
		path := b.callbackURL(start, testUser) + "&returnTo=https%3A%2F%2Fevil.com&redirect=%2F%2Fevil.com&next=%2Fstats"
		cb := b.get(path)
		if cb.StatusCode != http.StatusFound || cb.Header.Get("Location") != "/KT7P" {
			t.Errorf("callback = %d Location %q, want 302 /KT7P (the room of the signed transaction)", cb.StatusCode, cb.Header.Get("Location"))
		}
	})
	t.Run("the session of a new login replaces an older one", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		first := b.cookie("tf_session").Value
		e.clock.Advance(time.Hour)
		b.login("/", testUser)
		if b.cookie("tf_session").Value == first {
			t.Error("the session cookie was not renewed by a new login")
		}
	})
	t.Run("a login does not touch the guest cookie", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.get("/api/session")
		guest := b.cookie("tf_guest").Value
		b.login("/", testUser)
		if got := b.cookie("tf_guest"); got == nil || got.Value != guest {
			t.Error("the guest cookie changed with the login")
		}
	})
	t.Run("the profile is kept across logins", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		if r := b.request(http.MethodPut, "/api/me/profile", validProfile); r.StatusCode != http.StatusNoContent {
			t.Fatalf("PUT profile = %d %s", r.StatusCode, r.body)
		}
		b2 := e.browser()
		b2.login("/", authtest.User{Sub: testUser.Sub, Name: "Ana Maria"})
		var me api.UserInfo
		b2.get("/api/me").json(t, &me)
		if me.Name != "Ana Maria" || me.Profile == nil || me.Profile.Nickname != "Anita" {
			t.Errorf("me = %+v", me)
		}
	})
}

func TestGoogleCallbackFailures(t *testing.T) {
	wantRedirect := func(t *testing.T, e *acctEnv, b *browser, r reply, location string) {
		t.Helper()
		if r.StatusCode != http.StatusFound || r.Header.Get("Location") != location {
			t.Errorf("= %d Location %q, want 302 %q", r.StatusCode, r.Header.Get("Location"), location)
		}
		if len(r.body) != 0 {
			t.Errorf("the redirect has a body: %q", r.body)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control = %q", r.Header.Get("Cache-Control"))
		}
		if b.cookie("tf_session") != nil {
			t.Error("a failed login set a session")
		}
		for _, h := range r.Header.Values("Set-Cookie") {
			if strings.HasPrefix(h, "tf_session=") {
				t.Errorf("a failed login set %q", h)
			}
		}
	}

	t.Run("no transaction cookie", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		start := b.loginStart("/")
		path := b.callbackURL(start, testUser)
		other := e.browser() // another browser: it has no tf_oauth
		wantRedirect(t, e, other, other.get(path), "/?login=failed")
		if e.idp.Hits("/token") != 0 {
			t.Error("the code was exchanged without a transaction")
		}
		if got := e.metric("failed"); got != 1 {
			t.Errorf("logins_total{failed} = %v", got)
		}
	})
	t.Run("state mismatch", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		path := b.callbackURL(b.loginStart("/"), testUser)
		u, _ := url.Parse(path)
		q := u.Query()
		q.Set("state", "tampered")
		wantRedirect(t, e, b, b.get(u.Path+"?"+q.Encode()), "/?login=failed")
		if b.cookie("tf_oauth") != nil {
			t.Error("the transaction cookie was not cleared by a failed callback")
		}
	})
	t.Run("expired transaction", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		path := b.callbackURL(b.loginStart("/"), testUser)
		e.clock.Advance(11 * time.Minute)
		wantRedirect(t, e, b, b.get(path), "/?login=failed")
	})
	t.Run("replayed callback", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		path := b.callbackURL(b.loginStart("/"), testUser)
		if r := b.get(path); r.Header.Get("Location") != "/" {
			t.Fatalf("first callback = %d %q", r.StatusCode, r.Header.Get("Location"))
		}
		replay := e.browser() // the attacker has the URL but not the cookie
		wantRedirect(t, e, replay, replay.get(path), "/?login=failed")
	})
	t.Run("user denied", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		start := b.loginStart("/KT7P")
		deny, _ := e.idp.Deny(start.Header.Get("Location"))
		u, _ := url.Parse(deny)
		wantRedirect(t, e, b, b.get(u.RequestURI()), "/?login=cancelled")
		if got := e.metric("cancelled"); got != 1 {
			t.Errorf("logins_total{cancelled} = %v", got)
		}
	})
	t.Run("another provider error", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		start := b.loginStart("/")
		u, _ := url.Parse(start.Header.Get("Location"))
		wantRedirect(t, e, b, b.get("/api/auth/google/callback?error=server_error&state="+url.QueryEscape(u.Query().Get("state"))), "/?login=failed")
	})
	t.Run("token endpoint error", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		e.idp.OnToken(func(s *authtest.TokenSpec) { s.Status = http.StatusInternalServerError })
		b := e.browser()
		path := b.callbackURL(b.loginStart("/"), testUser)
		wantRedirect(t, e, b, b.get(path), "/?login=failed")
	})
	t.Run("invalid id token", func(t *testing.T) {
		for name, tweak := range map[string]func(*authtest.TokenSpec){
			"wrong nonce":    func(s *authtest.TokenSpec) { s.Claims["nonce"] = "x" },
			"wrong audience": func(s *authtest.TokenSpec) { s.Claims["aud"] = "x" },
			"wrong issuer":   func(s *authtest.TokenSpec) { s.Claims["iss"] = "https://evil.example" },
			"expired":        func(s *authtest.TokenSpec) { s.Claims["exp"] = time.Now().Add(-time.Hour).Unix() },
			"bad signature":  func(s *authtest.TokenSpec) { s.CorruptSignature = true },
			"alg none":       func(s *authtest.TokenSpec) { s.Header["alg"] = "none"; s.Unsigned = true },
		} {
			e := newAcctEnv(t, acctOpts{})
			e.idp.OnToken(tweak)
			b := e.browser()
			path := b.callbackURL(b.loginStart("/"), testUser)
			wantRedirect(t, e, b, b.get(path), "/?login=failed")
			if _, status := b.me(); status != http.StatusUnauthorized {
				t.Errorf("%s: logged in (%d)", name, status)
			}
		}
	})
	t.Run("the store fails", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		e.accounts.set("upsert", errors.New("database down: password=hunter2"))
		b := e.browser()
		path := b.callbackURL(b.loginStart("/"), testUser)
		wantRedirect(t, e, b, b.get(path), "/?login=failed")
		if got := e.metric("failed"); got != 1 {
			t.Errorf("logins_total{failed} = %v", got)
		}
		// Back to normal: the next login works.
		e.accounts.set("upsert", nil)
		if loc := b.login("/", testUser); loc != "/" {
			t.Errorf("login after the recovery went to %q", loc)
		}
	})
	t.Run("the provider is down when the login starts", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		e.idp.SetDown(true)
		b := e.browser()
		wantRedirect(t, e, b, b.loginStart("/"), "/?login=failed")
		if b.cookie("tf_oauth") != nil {
			t.Error("a failed start set the transaction cookie")
		}
		e.idp.SetDown(false)
		if loc := b.login("/", testUser); loc != "/" {
			t.Errorf("login once the provider is up went to %q", loc)
		}
	})
}

func TestGoogleCallbackNeverEchoesInput(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	payload := `<script>alert(1)</script>"'><img src=x onerror=alert(2)>`
	b.loginStart("/")
	for _, target := range []string{
		"/api/auth/google/callback?code=" + url.QueryEscape(payload) + "&state=" + url.QueryEscape(payload),
		"/api/auth/google/callback?error=" + url.QueryEscape(payload) + "&state=" + url.QueryEscape(payload),
		"/api/auth/google/callback?error=access_denied&error_description=" + url.QueryEscape(payload),
		"/api/auth/google/callback?" + url.QueryEscape(payload) + "=1",
		"/api/auth/google/callback?returnTo=https://evil.com&redirect=https://evil.com&next=//evil.com",
	} {
		r := b.get(target)
		if r.StatusCode != http.StatusFound {
			t.Errorf("GET %.60s = %d", target, r.StatusCode)
		}
		loc := r.Header.Get("Location")
		if loc != "/?login=failed" && loc != "/?login=cancelled" {
			t.Errorf("Location = %q", loc)
		}
		if len(r.body) != 0 || strings.Contains(strings.Join(r.Header.Values("Set-Cookie"), ""), "script") {
			t.Errorf("the response echoes input: body %q, cookies %v", r.body, r.Header.Values("Set-Cookie"))
		}
	}
	if strings.Contains(e.logs.String(), "script") && strings.Contains(e.logs.String(), "alert") {
		t.Error("attacker-controlled text was logged")
	}
}

func TestGoogleLoginRateLimit(t *testing.T) {
	e := newAcctEnv(t, acctOpts{limits: Limits{AuthRate: 1, AuthBurst: 4}})
	b := e.browser()
	// Login and callback share one bucket.
	for i := range 2 {
		if r := b.loginStart("/"); r.StatusCode != http.StatusFound {
			t.Fatalf("login %d = %d", i, r.StatusCode)
		}
	}
	for i := range 2 {
		r := b.get("/api/auth/google/callback?code=x&state=y")
		if r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/?login=failed" {
			t.Fatalf("callback %d = %d %q", i, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// The bucket is empty: login answers 429, the callback stays a redirect.
	r := b.loginStart("/")
	if r.StatusCode != http.StatusTooManyRequests || r.errCode(t) != "rate_limited" || r.Header.Get("Retry-After") == "" {
		t.Errorf("login over the limit = %d %s (Retry-After %q)", r.StatusCode, r.body, r.Header.Get("Retry-After"))
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", r.Header.Get("Cache-Control"))
	}
	cb := b.get("/api/auth/google/callback?code=x&state=y")
	if cb.StatusCode != http.StatusFound || cb.Header.Get("Location") != "/?login=failed" || len(cb.body) != 0 {
		t.Errorf("callback over the limit = %d %q %q", cb.StatusCode, cb.Header.Get("Location"), cb.body)
	}

	// It refills.
	e.clock.Advance(2 * time.Second)
	if r := b.loginStart("/"); r.StatusCode != http.StatusFound {
		t.Errorf("login after the refill = %d", r.StatusCode)
	}
}

// The transaction cookie of a login that started is still on the browser when
// its callback is turned away by the limiter; it must not stay valid.
func TestRateLimitedCallbackClearsTheTransactionCookie(t *testing.T) {
	e := newAcctEnv(t, acctOpts{limits: Limits{AuthRate: 1, AuthBurst: 1}})
	b := e.browser()
	if r := b.loginStart("/"); r.StatusCode != http.StatusFound {
		t.Fatalf("login = %d", r.StatusCode)
	}
	if b.cookie("tf_oauth") == nil {
		t.Fatal("the login did not set the transaction cookie")
	}
	cb := b.get("/api/auth/google/callback?code=x&state=y") // the bucket is empty now
	if cb.StatusCode != http.StatusFound || cb.Header.Get("Location") != "/?login=failed" {
		t.Fatalf("callback = %d %q", cb.StatusCode, cb.Header.Get("Location"))
	}
	if b.cookie("tf_oauth") != nil {
		t.Error("a rate-limited callback left the transaction cookie alive")
	}
}

func TestGoogleLoginRateLimitIsGlobalWithoutTrustedProxy(t *testing.T) {
	e := newAcctEnv(t, acctOpts{limits: Limits{AuthRate: 0.001, AuthBurst: 3}})
	// Different X-Forwarded-For values do not buy a fresh bucket when the
	// header is not trusted.
	ok := 0
	for i := range 10 {
		b := e.browser()
		r := b.request(http.MethodGet, "/api/auth/google/login", "", "X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		if r.StatusCode == http.StatusFound {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("%d logins passed with a burst of 3 and spoofed X-Forwarded-For", ok)
	}
}

const validProfile = `{"nickname":"Anita","avatar":{"emoji":"🍓","color":"#ff0066"}}`

func TestMeRequiresALogin(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/me", ""},
		{http.MethodPut, "/api/me/profile", validProfile},
		{http.MethodGet, "/api/me/stats", ""},
		{http.MethodDelete, "/api/me", ""},
	} {
		r := b.request(tc.method, tc.path, tc.body)
		if r.StatusCode != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
			t.Errorf("%s %s = %d %s, want 401 unauthorized", tc.method, tc.path, r.StatusCode, r.body)
		}
		if r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s: headers %v", tc.method, tc.path, r.Header)
		}
		if len(r.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("%s %s: set cookies %v although there was no session", tc.method, tc.path, r.Header.Values("Set-Cookie"))
		}
	}
}

func TestMeWithAnInvalidOrStaleSession(t *testing.T) {
	t.Run("a garbled cookie is cleared", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		r := e.rawGet(t, "/api/me", "tf_session=garbage")
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("= %d", r.StatusCode)
		}
		if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "tf_session=") || !strings.Contains(h, "Max-Age=0") {
			t.Errorf("Set-Cookie = %q, want the session cookie expired", h)
		}
	})
	t.Run("an expired session is cleared", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		e.clock.Advance(31 * 24 * time.Hour)
		r := b.get("/api/me")
		if r.StatusCode != http.StatusUnauthorized || b.cookie("tf_session") != nil {
			t.Errorf("= %d, session cookie still held: %v", r.StatusCode, b.cookie("tf_session") != nil)
		}
	})
	t.Run("a user deleted elsewhere is a lost session, cookie cleared", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		var me api.UserInfo
		b.get("/api/me").json(t, &me)
		if err := e.mem.DeleteUser(context.Background(), me.ID); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		if err := e.sessions.Issue(rec, me.ID); err != nil { // a still-valid cookie of the deleted user
			t.Fatal(err)
		}
		cookie := rec.Result().Cookies()[0]
		for _, tc := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/me/stats", ""},
			{http.MethodPut, "/api/me/profile", validProfile},
			{http.MethodDelete, "/api/me", ""},
			{http.MethodGet, "/api/me", ""},
		} {
			r := e.rawRequest(t, tc.method, tc.path, tc.body, cookie.String())
			if r.StatusCode != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
				t.Errorf("%s %s = %d %s", tc.method, tc.path, r.StatusCode, r.body)
			}
			if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "Max-Age=0") {
				t.Errorf("%s %s: the cookie of a missing user was not cleared (%q)", tc.method, tc.path, h)
			}
		}
	})
}

// rawRequest sends a request with a verbatim Cookie header and the allowed Origin.
func (e *acctEnv) rawRequest(t *testing.T, method, target, body, cookie string) reply {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.ts.URL+target, rd)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Origin", acctOrigin)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return reply{resp, data}
}

func TestMeNameIsTheTrimmedDisplayName(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", authtest.User{Sub: "sub-long", Name: "  Maria de los Angeles Fernandez  "})
	var me api.UserInfo
	b.get("/api/me").json(t, &me)
	if me.Name != "Maria de los Ang" || len([]rune(me.Name)) > game.MaxNameLen {
		t.Errorf("name = %q", me.Name)
	}
}

func TestProfile(t *testing.T) {
	loggedIn := func(t *testing.T) (*acctEnv, *browser) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		return e, b
	}

	t.Run("save and read back", func(t *testing.T) {
		_, b := loggedIn(t)
		r := b.request(http.MethodPut, "/api/me/profile", `{"nickname":"  Ana   Maria ","avatar":{"emoji":"🍉","color":"#112233"}}`)
		if r.StatusCode != http.StatusNoContent || len(r.body) != 0 {
			t.Fatalf("PUT = %d %q", r.StatusCode, r.body)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control = %q", r.Header.Get("Cache-Control"))
		}
		var me api.UserInfo
		b.get("/api/me").json(t, &me)
		want := &api.Profile{Nickname: "Ana Maria", Avatar: game.Avatar{Emoji: "🍉", Color: "#112233"}}
		if me.Profile == nil || *me.Profile != *want {
			t.Errorf("profile = %+v, want %+v (nickname cleaned like a join does)", me.Profile, want)
		}
		if me.Name != "Ana García" {
			t.Errorf("saving a profile changed the Google name: %q", me.Name)
		}

		// Replace it.
		b.request(http.MethodPut, "/api/me/profile", validProfile)
		b.get("/api/me").json(t, &me)
		if me.Profile.Nickname != "Anita" {
			t.Errorf("profile after the replace = %+v", me.Profile)
		}
	})

	t.Run("validation", func(t *testing.T) {
		_, b := loggedIn(t)
		b.request(http.MethodPut, "/api/me/profile", validProfile)
		long := strings.Repeat("a", 17)
		bad := []struct{ name, body, code string }{
			{"empty nickname", `{"nickname":"","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"blank nickname", `{"nickname":"   ","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"17 characters", `{"nickname":"` + long + `","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"only invisible characters", `{"nickname":"\u200b\u200d","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"NUL in the nickname", `{"nickname":"A\u0000B","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"control character", `{"nickname":"A\u0007B","avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"missing nickname", `{"avatar":{"emoji":"🍓","color":"#f06"}}`, "invalid_name"},
			{"empty emoji", `{"nickname":"Ana","avatar":{"emoji":"","color":"#f06"}}`, "invalid_avatar"},
			{"blank emoji", `{"nickname":"Ana","avatar":{"emoji":"  ","color":"#f06"}}`, "invalid_avatar"},
			{"empty color", `{"nickname":"Ana","avatar":{"emoji":"🍓","color":""}}`, "invalid_avatar"},
			{"missing avatar", `{"nickname":"Ana"}`, "invalid_avatar"},
			{"emoji over 32 bytes", `{"nickname":"Ana","avatar":{"emoji":"` + strings.Repeat("🍓", 9) + `","color":"#f06"}}`, "invalid_avatar"},
			{"color over 32 bytes", `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"` + strings.Repeat("c", 33) + `"}}`, "invalid_avatar"},
			{"NUL in the emoji", `{"nickname":"Ana","avatar":{"emoji":"a\u0000","color":"#f06"}}`, "invalid_avatar"},
			{"newline in the color", `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"#f\n06"}}`, "invalid_avatar"},
			{"name is checked before the avatar", `{"nickname":"","avatar":{"emoji":"","color":""}}`, "invalid_name"},
			{"not JSON", `nope`, "bad_request"},
			{"an array", `[]`, "bad_request"},
			{"unknown field", `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"#f06"},"admin":true}`, "bad_request"},
			{"unknown avatar field", `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"#f06","size":3}}`, "bad_request"},
			{"trailing data", validProfile + `{}`, "bad_request"},
			{"wrong types", `{"nickname":5,"avatar":{"emoji":"🍓","color":"#f06"}}`, "bad_request"},
		}
		for _, tc := range bad {
			r := b.request(http.MethodPut, "/api/me/profile", tc.body)
			if r.StatusCode != http.StatusBadRequest || r.errCode(t) != tc.code {
				t.Errorf("%s: %d %s, want 400 %s", tc.name, r.StatusCode, r.body, tc.code)
			}
		}
		// A body-less request is a bad request too.
		if r := b.request(http.MethodPut, "/api/me/profile", "", "Content-Type", "application/json"); r.StatusCode != http.StatusBadRequest || r.errCode(t) != "bad_request" {
			t.Errorf("empty body: %d %s", r.StatusCode, r.body)
		}
		// Nothing above changed what was saved.
		var me api.UserInfo
		b.get("/api/me").json(t, &me)
		if me.Profile == nil || me.Profile.Nickname != "Anita" {
			t.Errorf("a refused profile changed the saved one: %+v", me.Profile)
		}
	})

	t.Run("exactly 16 characters and 32 bytes are fine", func(t *testing.T) {
		_, b := loggedIn(t)
		body := `{"nickname":"` + strings.Repeat("ñ", 16) + `","avatar":{"emoji":"` + strings.Repeat("🍓", 8) + `","color":"` + strings.Repeat("c", 32) + `"}}`
		if r := b.request(http.MethodPut, "/api/me/profile", body); r.StatusCode != http.StatusNoContent {
			t.Errorf("= %d %s", r.StatusCode, r.body)
		}
	})

	t.Run("media type and size", func(t *testing.T) {
		_, b := loggedIn(t)
		for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "application/jsonx", "multipart/form-data"} {
			r := b.request(http.MethodPut, "/api/me/profile", validProfile, "Content-Type", ct)
			if r.StatusCode != http.StatusUnsupportedMediaType || r.errCode(t) != "unsupported_media_type" {
				t.Errorf("Content-Type %q = %d %s", ct, r.StatusCode, r.body)
			}
		}
		if r := b.request(http.MethodPut, "/api/me/profile", validProfile, "Content-Type", "application/json; charset=utf-8"); r.StatusCode != http.StatusNoContent {
			t.Errorf("a charset parameter = %d", r.StatusCode)
		}
		big := `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"#f06"},"pad":"` + strings.Repeat("x", 5000) + `"}`
		if r := b.request(http.MethodPut, "/api/me/profile", big); r.StatusCode != http.StatusRequestEntityTooLarge || r.errCode(t) != "payload_too_large" {
			t.Errorf("a 5 KB body = %d %s", r.StatusCode, r.body)
		}
	})

	t.Run("the store fails", func(t *testing.T) {
		e, b := loggedIn(t)
		e.accounts.set("save", errors.New("boom: password=hunter2"))
		r := b.request(http.MethodPut, "/api/me/profile", validProfile)
		if r.StatusCode != http.StatusInternalServerError || r.errCode(t) != "internal" {
			t.Errorf("= %d %s", r.StatusCode, r.body)
		}
		if strings.Contains(string(r.body), "hunter2") {
			t.Error("the store error leaked to the client")
		}
	})
}

func TestOriginIsEnforcedOnAccountWrites(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/me/profile", validProfile},
		{http.MethodDelete, "/api/me", ""},
		{http.MethodPost, "/api/auth/logout", ""},
	} {
		for name, origin := range map[string]string{"missing": "", "foreign": "https://evil.example", "null": "null", "wrong port": "http://localhost:9999"} {
			r := b.request(tc.method, tc.path, tc.body, "Origin", origin)
			if r.StatusCode != http.StatusForbidden || r.errCode(t) != "forbidden_origin" {
				t.Errorf("%s %s with %s Origin = %d %s", tc.method, tc.path, name, r.StatusCode, r.body)
			}
		}
	}
	// Nothing was changed or logged out by those.
	var me api.UserInfo
	if r := b.get("/api/me"); r.StatusCode != http.StatusOK {
		t.Fatalf("the refused requests changed the session: %d", r.StatusCode)
	} else {
		r.json(t, &me)
	}
	if me.Profile != nil {
		t.Error("a forbidden PUT saved a profile")
	}
}

func TestStats(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	var me api.UserInfo
	b.get("/api/me").json(t, &me)

	r := b.get("/api/me/stats")
	if r.StatusCode != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"gamesPlayed":0,"wins":0,"totalPoints":0,"bestRound":0,"uniqueAnswers":0}` {
		t.Errorf("zero stats = %d %s", r.StatusCode, r.body)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", r.Header.Get("Cache-Control"))
	}

	err := e.mem.RecordGame(context.Background(), store.GameRecord{
		ID:      "00000000-0000-4000-8000-000000000001",
		Results: []store.UserResult{{UserID: me.ID, Won: true, Points: 85, BestRound: 40, UniqueAnswers: 6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got api.Stats
	b.get("/api/me/stats").json(t, &got)
	if want := (api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 85, BestRound: 40, UniqueAnswers: 6}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}

	e.accounts.set("stats", errors.New("boom"))
	if r := b.get("/api/me/stats"); r.StatusCode != http.StatusInternalServerError || r.errCode(t) != "internal" {
		t.Errorf("with the store failing = %d %s", r.StatusCode, r.body)
	}
}

func TestDeleteAccount(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	var me api.UserInfo
	b.get("/api/me").json(t, &me)
	b.request(http.MethodPut, "/api/me/profile", validProfile)
	_ = e.mem.RecordGame(context.Background(), store.GameRecord{
		ID: "00000000-0000-4000-8000-000000000001", Results: []store.UserResult{{UserID: me.ID, Points: 10}},
	})
	b.get("/api/session") // the guest cookie exists
	guest := b.cookie("tf_guest").Value

	r := b.request(http.MethodDelete, "/api/me", "")
	if r.StatusCode != http.StatusNoContent || len(r.body) != 0 {
		t.Fatalf("DELETE = %d %q", r.StatusCode, r.body)
	}
	if b.cookie("tf_session") != nil {
		t.Error("the session cookie survived the deletion")
	}
	if c := b.cookie("tf_guest"); c == nil || c.Value != guest {
		t.Error("deleting the account touched the guest cookie")
	}
	if _, err := e.mem.GetUser(context.Background(), me.ID); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("the user is still in the store: %v", err)
	}
	if _, err := e.mem.UserStats(context.Background(), me.ID); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("the stats are still in the store: %v", err)
	}
	if r := b.get("/api/me"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/me after the deletion = %d", r.StatusCode)
	}

	// A copy of the old cookie (another device) is a lost session now.
	rec := httptest.NewRecorder()
	_ = e.sessions.Issue(rec, me.ID)
	r = e.rawRequest(t, http.MethodDelete, "/api/me", "", rec.Result().Cookies()[0].String())
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("deleting twice = %d, want 401", r.StatusCode)
	}

	// Logging in with the same Google account starts a new, empty account.
	b2 := e.browser()
	b2.login("/", testUser)
	var again api.UserInfo
	b2.get("/api/me").json(t, &again)
	var stats api.Stats
	b2.get("/api/me/stats").json(t, &stats)
	if again.ID == me.ID || again.Profile != nil || stats != (api.Stats{}) {
		t.Errorf("the new account = %+v %+v: nothing may survive", again, stats)
	}

	e.accounts.set("delete", errors.New("boom"))
	if r := b2.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusInternalServerError || r.errCode(t) != "internal" {
		t.Errorf("with the store failing = %d %s", r.StatusCode, r.body)
	}
	if b2.cookie("tf_session") == nil {
		t.Error("a failed deletion logged the user out")
	}
}

func TestLogout(t *testing.T) {
	t.Run("clears the session", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.login("/", testUser)
		r := b.request(http.MethodPost, "/api/auth/logout", "")
		if r.StatusCode != http.StatusNoContent || len(r.body) != 0 {
			t.Fatalf("= %d %q", r.StatusCode, r.body)
		}
		if b.cookie("tf_session") != nil {
			t.Error("the session cookie survived the logout")
		}
		if r := b.get("/api/me"); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("/api/me after the logout = %d", r.StatusCode)
		}
		// The account itself is untouched.
		b.login("/", testUser)
		if r := b.get("/api/me"); r.StatusCode != http.StatusOK {
			t.Errorf("logging in again = %d", r.StatusCode)
		}
	})
	t.Run("works without a session", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		r := e.browser().request(http.MethodPost, "/api/auth/logout", "")
		if r.StatusCode != http.StatusNoContent {
			t.Errorf("= %d", r.StatusCode)
		}
		if h := strings.Join(r.Header.Values("Set-Cookie"), ";"); !strings.Contains(h, "tf_session=") || !strings.Contains(h, "Max-Age=0") {
			t.Errorf("Set-Cookie = %q", h)
		}
	})
	t.Run("does not touch the guest cookie", func(t *testing.T) {
		e := newAcctEnv(t, acctOpts{})
		b := e.browser()
		b.get("/api/session")
		guest := b.cookie("tf_guest").Value
		b.request(http.MethodPost, "/api/auth/logout", "")
		if c := b.cookie("tf_guest"); c == nil || c.Value != guest {
			t.Error("logging out touched the guest cookie")
		}
	})
}

func TestAccountRoutesNeedTheirDependencies(t *testing.T) {
	e := newAcctEnv(t, acctOpts{noAccounts: true})
	b := e.browser()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/me"},
		{http.MethodGet, "/api/me/stats"},
		{http.MethodPut, "/api/me/profile"},
		{http.MethodDelete, "/api/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodGet, "/api/auth/google/login"},
		{http.MethodGet, "/api/auth/google/callback"},
	} {
		r := b.request(tc.method, tc.path, "")
		if r.StatusCode != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Errorf("%s %s without accounts = %d %s, want 404 not_found", tc.method, tc.path, r.StatusCode, r.body)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	for _, tc := range []struct {
		method, path, body string
		origin             bool
	}{
		{http.MethodGet, "/api/session", "", false},
		{http.MethodGet, "/api/version", "", false},
		{http.MethodGet, "/api/categories", "", false},
		{http.MethodGet, "/api/nothing-here", "", false},
		{http.MethodGet, "/api/rooms/ZZZZ", "", false},
		{http.MethodPost, "/api/rooms", `{}`, true},
		{http.MethodGet, "/api/me", "", false},
		{http.MethodGet, "/api/me/stats", "", false},
		{http.MethodPut, "/api/me/profile", validProfile, true},
		{http.MethodPost, "/api/auth/logout", "", true},
		{http.MethodGet, "/api/auth/google/login", "", false},
		{http.MethodGet, "/api/auth/google/callback", "", false},
		{http.MethodPost, "/api/rooms", `{}`, false}, // 403 forbidden_origin
		{http.MethodGet, "/healthz", "", false},
		{http.MethodGet, "/readyz", "", false},
		{http.MethodGet, "/metrics", "", false},
	} {
		hdr := []string{}
		if !tc.origin && tc.method != http.MethodGet {
			hdr = append(hdr, "Origin", "")
		}
		r := b.request(tc.method, tc.path, tc.body, hdr...)
		if got := r.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s %s (%d): X-Content-Type-Options = %q", tc.method, tc.path, r.StatusCode, got)
		}
		if got := r.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("%s %s (%d): Referrer-Policy = %q", tc.method, tc.path, r.StatusCode, got)
		}
		cc := r.Header.Get("Cache-Control")
		switch {
		case tc.path == "/api/categories":
			if cc != "public, max-age=3600" {
				t.Errorf("%s: Cache-Control = %q, the handler's own value must win", tc.path, cc)
			}
		case strings.HasPrefix(tc.path, "/api/"):
			if cc != "no-store" {
				t.Errorf("%s %s (%d): Cache-Control = %q, want no-store", tc.method, tc.path, r.StatusCode, cc)
			}
		}
	}
}

func TestSessionCookieValueIsAMAC(t *testing.T) {
	e := newAcctEnv(t, acctOpts{})
	b := e.browser()
	b.login("/", testUser)
	var me api.UserInfo
	b.get("/api/me").json(t, &me)
	parts := strings.Split(b.cookie("tf_session").Value, ".")
	if len(parts) != 3 || parts[0] != me.ID {
		t.Errorf("tf_session = %q, want <userId>.<expiry>.<mac>", b.cookie("tf_session").Value)
	}
	if len(parts) == 3 && fmt.Sprint(len(parts[2])) != "43" {
		t.Errorf("mac has %d characters", len(parts[2]))
	}
}
