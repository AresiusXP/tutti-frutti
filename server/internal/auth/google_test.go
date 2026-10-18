package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
)

const (
	testClientID     = "123-test.apps.googleusercontent.com"
	testClientSecret = "fake-test-client-secret-0123456789"
	testRedirectURL  = "http://localhost:8000/api/auth/google/callback"
)

// testClock is the clock shared by the provider and the server.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type loginEnv struct {
	t     *testing.T
	idp   *authtest.IdP
	g     *Google
	clock *testClock
	logs  *syncBuffer
}

func newLoginEnv(t *testing.T, opts ...func(*GoogleConfig)) *loginEnv {
	t.Helper()
	clock := &testClock{t: time.Now()}
	idp := authtest.New(t, testClientID, testClientSecret)
	idp.SetClock(clock.Now)
	logs := &syncBuffer{}
	cfg := GoogleConfig{
		ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testRedirectURL,
		Secret: []byte(testSecret), Issuer: idp.URL, Now: clock.Now,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &loginEnv{t: t, idp: idp, g: NewGoogle(cfg), clock: clock, logs: logs}
}

// started is the result of the first leg of a login.
type started struct {
	authURL string
	cookie  *http.Cookie // the tf_oauth cookie, nil when none was set
	err     error
	rec     *httptest.ResponseRecorder
}

func (e *loginEnv) start(returnTo string) started {
	e.t.Helper()
	rec := httptest.NewRecorder()
	authURL, err := e.g.Start(context.Background(), rec, returnTo)
	s := started{authURL: authURL, err: err, rec: rec}
	for _, c := range rec.Result().Cookies() {
		if c.Name == OAuthCookie {
			s.cookie = c
		}
	}
	return s
}

// callbackRequest is the browser coming back from the provider.
func callbackRequest(callbackURL string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

type finished struct {
	id        Identity
	returnTo  string
	err       error
	clearedTx bool
}

func (e *loginEnv) finish(r *http.Request) finished {
	e.t.Helper()
	rec := httptest.NewRecorder()
	id, returnTo, err := e.g.Finish(context.Background(), rec, r)
	f := finished{id: id, returnTo: returnTo, err: err}
	for _, c := range rec.Result().Cookies() {
		if c.Name == OAuthCookie && c.Value == "" && c.MaxAge < 0 {
			f.clearedTx = true
		}
	}
	return f
}

// browse runs both legs of a login for user, like a browser would.
func (e *loginEnv) browse(returnTo string, user authtest.User) finished {
	e.t.Helper()
	s := e.start(returnTo)
	if s.err != nil {
		e.t.Fatalf("Start: %v", s.err)
	}
	callback, err := e.idp.Authorize(s.authURL, user)
	if err != nil {
		e.t.Fatalf("provider refused the authorization request: %v", err)
	}
	return e.finish(callbackRequest(callback, s.cookie))
}

var ana = authtest.User{Sub: "110169484474386276334", Name: "Ana García"}

func wantFailed(t *testing.T, f finished) {
	t.Helper()
	if !errors.Is(f.err, ErrLoginFailed) || errors.Is(f.err, ErrLoginCancelled) {
		t.Errorf("error = %v, want ErrLoginFailed", f.err)
	}
	if f.id != (Identity{}) || f.returnTo != "" {
		t.Errorf("a failed login returned %+v, %q", f.id, f.returnTo)
	}
	if !f.clearedTx {
		t.Error("the transaction cookie was not expired")
	}
}

func TestGoogleLoginSuccess(t *testing.T) {
	e := newLoginEnv(t)
	f := e.browse("/kt7p", ana)
	if f.err != nil {
		t.Fatalf("Finish: %v", f.err)
	}
	if f.id.Subject != ana.Sub || f.id.Name != "Ana García" {
		t.Errorf("identity = %+v", f.id)
	}
	if f.returnTo != "/KT7P" {
		t.Errorf("returnTo = %q, want /KT7P (normalised)", f.returnTo)
	}
	if !f.clearedTx {
		t.Error("the transaction cookie was not expired on success")
	}
	if n := e.idp.Hits("/token"); n != 1 {
		t.Errorf("token endpoint hits = %d, want 1", n)
	}
}

func TestGoogleStartBuildsTheAuthorizationRequest(t *testing.T) {
	e := newLoginEnv(t)
	s := e.start("/KT7P")
	if s.err != nil {
		t.Fatal(s.err)
	}
	u, err := url.Parse(s.authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != e.idp.URL+"/auth" {
		t.Errorf("the browser is sent to %q, want the discovered authorization endpoint", got)
	}
	q := u.Query()
	tx, ok := e.g.parseTx(s.cookie.Value)
	if !ok {
		t.Fatal("the transaction cookie does not parse")
	}
	sum := sha256.Sum256([]byte(tx.Verifier))
	want := map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirectURL,
		"scope":                 "openid profile",
		"state":                 tx.State,
		"nonce":                 tx.Nonce,
		"code_challenge":        enc.EncodeToString(sum[:]),
		"code_challenge_method": "S256",
		"prompt":                "select_account",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if len(q) != len(want) {
		t.Errorf("the authorization request has parameters %v, want exactly %d (no email scope, no offline access)", q, len(want))
	}
	if strings.Contains(s.authURL, "email") || strings.Contains(s.authURL, tx.Verifier) {
		t.Error("the authorization URL leaks the email scope or the PKCE verifier")
	}
	if tx.ReturnTo != "/KT7P" {
		t.Errorf("returnTo in the transaction = %q", tx.ReturnTo)
	}
	for name, v := range map[string]string{"state": tx.State, "nonce": tx.Nonce, "verifier": tx.Verifier} {
		if len(v) != secretLen {
			t.Errorf("%s has %d characters, want %d", name, len(v), secretLen)
		}
	}
	if tx.State == tx.Nonce || tx.State == tx.Verifier || tx.Nonce == tx.Verifier {
		t.Error("state, nonce and verifier must be independent random values")
	}
}

func TestGoogleTransactionIsUniquePerLogin(t *testing.T) {
	e := newLoginEnv(t)
	a, b := e.start("/"), e.start("/")
	ta, _ := e.g.parseTx(a.cookie.Value)
	tb, _ := e.g.parseTx(b.cookie.Value)
	if ta.State == tb.State || ta.Nonce == tb.Nonce || ta.Verifier == tb.Verifier {
		t.Error("two logins share a secret")
	}
}

func TestGoogleTransactionCookieAttributes(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			e := newLoginEnv(t, func(c *GoogleConfig) { c.Secure = secure })
			c := e.start("/").cookie
			if c == nil {
				t.Fatal("no transaction cookie")
			}
			if c.Name != "tf_oauth" || c.Path != "/api/auth/google" || c.MaxAge != 600 {
				t.Errorf("cookie = %+v", c)
			}
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != secure {
				t.Errorf("HttpOnly=%v SameSite=%v Secure=%v", c.HttpOnly, c.SameSite, c.Secure)
			}
			if len(c.Value) > maxOAuthCookieLen {
				t.Errorf("cookie has %d bytes, more than the %d the server accepts", len(c.Value), maxOAuthCookieLen)
			}
		})
	}
}

func TestGoogleReturnToIsSanitised(t *testing.T) {
	for in, want := range map[string]string{
		"/":                 "/",
		"/KT7P":             "/KT7P",
		"/kt7p":             "/KT7P",
		"//evil.com":        "/",
		"https://evil.com/": "/",
		"":                  "/",
	} {
		e := newLoginEnv(t)
		if f := e.browse(in, ana); f.err != nil || f.returnTo != want {
			t.Errorf("returnTo %q -> %q (%v), want %q", in, f.returnTo, f.err, want)
		}
	}
}

func TestGoogleStateChecks(t *testing.T) {
	t.Run("no transaction cookie", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		wantFailed(t, e.finish(callbackRequest(cb)))
		if n := e.idp.Hits("/token"); n != 0 {
			t.Errorf("the code was exchanged (%d token requests) without a valid transaction", n)
		}
	})
	t.Run("the state of another login", func(t *testing.T) {
		e := newLoginEnv(t)
		a, b := e.start("/"), e.start("/")
		cb, _ := e.idp.Authorize(a.authURL, ana)
		wantFailed(t, e.finish(callbackRequest(cb, b.cookie)))
		if n := e.idp.Hits("/token"); n != 0 {
			t.Errorf("token requests = %d, want 0", n)
		}
	})
	t.Run("state missing, empty, altered or padded", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		u, _ := url.Parse(cb)
		state := u.Query().Get("state")
		for name, mutate := range map[string]func(url.Values){
			"missing":    func(q url.Values) { q.Del("state") },
			"empty":      func(q url.Values) { q.Set("state", "") },
			"last char":  func(q url.Values) { q.Set("state", state[:len(state)-1]+flip(state[len(state)-1])) },
			"first char": func(q url.Values) { q.Set("state", flip(state[0])+state[1:]) },
			"truncated":  func(q url.Values) { q.Set("state", state[:len(state)-1]) },
			"padded":     func(q url.Values) { q.Set("state", state+"A") },
			"twice":      func(q url.Values) { q.Add("state", "x") }, // Get takes the first: still right
		} {
			q := u.Query()
			mutate(q)
			u2 := *u
			u2.RawQuery = q.Encode()
			f := e.finish(callbackRequest(u2.String(), s.cookie))
			if name == "twice" {
				if f.err != nil {
					t.Errorf("%s: %v", name, f.err)
				}
				continue
			}
			if !errors.Is(f.err, ErrLoginFailed) {
				t.Errorf("%s: error = %v, want ErrLoginFailed", name, f.err)
			}
		}
	})
	t.Run("expired transaction", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		e.clock.Advance(OAuthTTL + time.Second)
		wantFailed(t, e.finish(callbackRequest(cb, s.cookie)))
	})
	t.Run("a transaction one second from expiring is fine", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		e.clock.Advance(OAuthTTL - time.Second)
		if f := e.finish(callbackRequest(cb, s.cookie)); f.err != nil {
			t.Errorf("Finish: %v", f.err)
		}
	})
	t.Run("tampered, truncated or oversized cookie", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		values := map[string]string{
			"empty":     "",
			"truncated": s.cookie.Value[:len(s.cookie.Value)-1],
			"half":      s.cookie.Value[:len(s.cookie.Value)/2],
			"no mac":    s.cookie.Value[:strings.Index(s.cookie.Value, ".")],
			"oversized": s.cookie.Value + strings.Repeat("A", 5000),
			"doubled":   s.cookie.Value + s.cookie.Value,
		}
		body, mac, _ := strings.Cut(s.cookie.Value, ".")
		values["payload changed"] = flip(body[10]) + body[:10] + body[11:] + "." + mac
		values["mac changed"] = body + "." + flip(mac[0]) + mac[1:]
		for name, v := range values {
			r := httptest.NewRequest(http.MethodGet, cb, nil)
			r.Header.Set("Cookie", OAuthCookie+"="+v)
			if f := e.finish(r); !errors.Is(f.err, ErrLoginFailed) {
				t.Errorf("%s: error = %v", name, f.err)
			}
		}
		if n := e.idp.Hits("/token"); n != 0 {
			t.Errorf("token requests = %d, want 0", n)
		}
	})
	t.Run("signed with another secret", func(t *testing.T) {
		e := newLoginEnv(t)
		other := NewGoogle(GoogleConfig{
			ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testRedirectURL,
			Secret: []byte("another-secret-that-is-long-enough-abc"), Issuer: e.idp.URL, Now: e.clock.Now,
		})
		rec := httptest.NewRecorder()
		authURL, err := other.Start(context.Background(), rec, "/")
		if err != nil {
			t.Fatal(err)
		}
		cb, _ := e.idp.Authorize(authURL, ana)
		wantFailed(t, e.finish(callbackRequest(cb, rec.Result().Cookies()[0])))
	})
	t.Run("a transaction with a returnTo outside the allow-list", func(t *testing.T) {
		e := newLoginEnv(t)
		for _, rt := range []string{"//evil.com", "https://evil.com", "/stats", ""} {
			tx := oauthTx{State: randomToken(), Verifier: randomToken(), Nonce: randomToken(), ReturnTo: rt, Expires: e.clock.Now().Add(OAuthTTL).Unix()}
			if _, ok := e.g.parseTx(e.g.signTx(tx)); ok {
				t.Errorf("a signed transaction with returnTo %q was accepted", rt)
			}
		}
	})
	t.Run("malformed secrets in a signed transaction", func(t *testing.T) {
		e := newLoginEnv(t)
		good := oauthTx{State: randomToken(), Verifier: randomToken(), Nonce: randomToken(), ReturnTo: "/", Expires: e.clock.Now().Add(OAuthTTL).Unix()}
		if _, ok := e.g.parseTx(e.g.signTx(good)); !ok {
			t.Fatal("a good transaction was refused")
		}
		for name, mutate := range map[string]func(*oauthTx){
			"short state":    func(x *oauthTx) { x.State = "short" },
			"empty verifier": func(x *oauthTx) { x.Verifier = "" },
			"long nonce":     func(x *oauthTx) { x.Nonce += "A" },
			"expiry in the far future": func(x *oauthTx) {
				x.Expires = e.clock.Now().Add(24 * time.Hour).Unix()
			},
			"no expiry": func(x *oauthTx) { x.Expires = 0 },
		} {
			x := good
			mutate(&x)
			if _, ok := e.g.parseTx(e.g.signTx(x)); ok {
				t.Errorf("%s: accepted", name)
			}
		}
	})
	t.Run("a signed non-JSON payload", func(t *testing.T) {
		e := newLoginEnv(t)
		body := enc.EncodeToString([]byte("not json"))
		if _, ok := e.g.parseTx(body + "." + enc.EncodeToString(signMAC(e.g.key, body))); ok {
			t.Error("accepted")
		}
		bad := "***"
		if _, ok := e.g.parseTx(bad + "." + enc.EncodeToString(signMAC(e.g.key, bad))); ok {
			t.Error("a payload that is not base64 was accepted")
		}
	})
}

func flip(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

func TestGooglePKCEBindsTheCodeToTheBrowserThatStartedTheLogin(t *testing.T) {
	e := newLoginEnv(t)
	victim, attacker := e.start("/"), e.start("/")
	// The attacker's authorization code (bound to the attacker's challenge)
	// delivered to the victim's browser with the victim's state.
	victimCB, _ := e.idp.Authorize(victim.authURL, ana)
	attackerCB, _ := e.idp.Authorize(attacker.authURL, authtest.User{Sub: "attacker", Name: "Mallory"})
	vu, _ := url.Parse(victimCB)
	au, _ := url.Parse(attackerCB)
	q := vu.Query()
	q.Set("code", au.Query().Get("code"))
	vu.RawQuery = q.Encode()
	wantFailed(t, e.finish(callbackRequest(vu.String(), victim.cookie)))
}

func TestGoogleCodeIsSingleUse(t *testing.T) {
	e := newLoginEnv(t)
	s := e.start("/")
	cb, _ := e.idp.Authorize(s.authURL, ana)
	if f := e.finish(callbackRequest(cb, s.cookie)); f.err != nil {
		t.Fatalf("first use: %v", f.err)
	}
	// A replayed callback, cookie included (the browser never saw the expiry).
	wantFailed(t, e.finish(callbackRequest(cb, s.cookie)))
}

func TestGoogleTokenRequest(t *testing.T) {
	e := newLoginEnv(t)
	s := e.start("/")
	cb, _ := e.idp.Authorize(s.authURL, ana)
	tx, _ := e.g.parseTx(s.cookie.Value)
	if f := e.finish(callbackRequest(cb, s.cookie)); f.err != nil {
		t.Fatal(f.err)
	}
	form := e.idp.LastTokenRequest()
	if form.Get("code_verifier") != tx.Verifier {
		t.Error("the token request does not carry the PKCE verifier")
	}
	if form.Get("redirect_uri") != testRedirectURL || form.Get("grant_type") != "authorization_code" {
		t.Errorf("token request = %v", form)
	}
}

func TestGoogleWrongClientSecret(t *testing.T) {
	e := newLoginEnv(t, func(c *GoogleConfig) { c.ClientSecret = "not-the-secret" })
	wantFailed(t, e.browse("/", ana))
	if strings.Contains(e.logs.String(), "not-the-secret") {
		t.Error("the client secret was logged")
	}
}

func TestGoogleUserDeniedOrProviderError(t *testing.T) {
	t.Run("access_denied is a cancellation", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Deny(s.authURL)
		f := e.finish(callbackRequest(cb, s.cookie))
		if !errors.Is(f.err, ErrLoginCancelled) || errors.Is(f.err, ErrLoginFailed) {
			t.Errorf("error = %v, want ErrLoginCancelled", f.err)
		}
		if !f.clearedTx {
			t.Error("the transaction cookie was not expired")
		}
		if n := e.idp.Hits("/token"); n != 0 {
			t.Errorf("token requests = %d", n)
		}
	})
	t.Run("access_denied without a valid transaction is a failure", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		cb, _ := e.idp.Deny(s.authURL)
		wantFailed(t, e.finish(callbackRequest(cb)))
	})
	t.Run("access_denied with a wrong state is a failure", func(t *testing.T) {
		e := newLoginEnv(t)
		s := e.start("/")
		wantFailed(t, e.finish(callbackRequest(testRedirectURL+"?error=access_denied&state=nope", s.cookie)))
	})
	for _, code := range []string{"server_error", "invalid_scope", "temporarily_unavailable", "login_required", "ACCESS_DENIED", strings.Repeat("x", 500)} {
		t.Run("other error "+truncate(code, 12), func(t *testing.T) {
			e := newLoginEnv(t)
			s := e.start("/")
			u, _ := url.Parse(s.authURL)
			cb := testRedirectURL + "?" + url.Values{"error": {code}, "state": {u.Query().Get("state")}}.Encode()
			wantFailed(t, e.finish(callbackRequest(cb, s.cookie)))
			if strings.Contains(e.logs.String(), strings.Repeat("x", 100)) {
				t.Error("an attacker-controlled error value was logged in full")
			}
		})
	}
}

func TestGoogleCallbackParameters(t *testing.T) {
	e := newLoginEnv(t)
	s := e.start("/")
	u, _ := url.Parse(s.authURL)
	state := u.Query().Get("state")
	for name, query := range map[string]string{
		"no code":      "state=" + state,
		"empty code":   "code=&state=" + state,
		"huge code":    "code=" + strings.Repeat("a", maxCodeLen+1) + "&state=" + state,
		"no parameter": "",
	} {
		f := e.finish(callbackRequest(testRedirectURL+"?"+query, s.cookie))
		if !errors.Is(f.err, ErrLoginFailed) {
			t.Errorf("%s: error = %v", name, f.err)
		}
	}
	if n := e.idp.Hits("/token"); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
}

// TestGoogleIDTokenValidation: every way an ID token can be wrong is a failed
// login, never a session.
func TestGoogleIDTokenValidation(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e := newLoginEnv(t)
	pub, err := x509.MarshalPKIXPublicKey(&e.idp.PrivateKey().PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return e.clock.Now() }

	bad := map[string]func(*authtest.TokenSpec){
		"wrong audience":           func(s *authtest.TokenSpec) { s.Claims["aud"] = "someone-else.apps.googleusercontent.com" },
		"audience list without us": func(s *authtest.TokenSpec) { s.Claims["aud"] = []string{"a", "b"} },
		"several audiences, bad azp": func(s *authtest.TokenSpec) {
			s.Claims["aud"] = []string{testClientID, "other"}
			s.Claims["azp"] = "other"
		},
		"one audience, foreign azp": func(s *authtest.TokenSpec) { s.Claims["azp"] = "other" },
		"several audiences, no azp": func(s *authtest.TokenSpec) {
			s.Claims["aud"] = []string{testClientID, "other"}
			delete(s.Claims, "azp")
		},
		"missing audience":          func(s *authtest.TokenSpec) { delete(s.Claims, "aud") },
		"wrong issuer":              func(s *authtest.TokenSpec) { s.Claims["iss"] = "https://evil.example.com" },
		"issuer without scheme":     func(s *authtest.TokenSpec) { s.Claims["iss"] = "accounts.google.com" },
		"missing issuer":            func(s *authtest.TokenSpec) { delete(s.Claims, "iss") },
		"expired":                   func(s *authtest.TokenSpec) { s.Claims["exp"] = now().Add(-time.Hour).Unix() },
		"expired a second ago":      func(s *authtest.TokenSpec) { s.Claims["exp"] = now().Add(-time.Second).Unix() },
		"missing expiry":            func(s *authtest.TokenSpec) { delete(s.Claims, "exp") },
		"wrong nonce":               func(s *authtest.TokenSpec) { s.Claims["nonce"] = "another-nonce" },
		"missing nonce":             func(s *authtest.TokenSpec) { delete(s.Claims, "nonce") },
		"empty nonce":               func(s *authtest.TokenSpec) { s.Claims["nonce"] = "" },
		"corrupt signature":         func(s *authtest.TokenSpec) { s.CorruptSignature = true },
		"signed by another key":     func(s *authtest.TokenSpec) { s.SignWith = otherKey },
		"alg none":                  func(s *authtest.TokenSpec) { s.Header["alg"] = "none"; s.Unsigned = true },
		"alg none with a signature": func(s *authtest.TokenSpec) { s.Header["alg"] = "none" },
		"alg HS256 keyed with the public key": func(s *authtest.TokenSpec) {
			s.Header["alg"] = "HS256"
			s.HMACKey = pub
		},
		"alg HS256 with an empty key": func(s *authtest.TokenSpec) { s.Header["alg"] = "HS256"; s.HMACKey = nil },
		"alg RS512, correctly signed": func(s *authtest.TokenSpec) { s.Header["alg"] = "RS512" },
		"alg ES256":                   func(s *authtest.TokenSpec) { s.Header["alg"] = "ES256" },
		"unknown key id":              func(s *authtest.TokenSpec) { s.Header["kid"] = "no-such-key" },
		"missing subject":             func(s *authtest.TokenSpec) { delete(s.Claims, "sub") },
		"empty subject":               func(s *authtest.TokenSpec) { s.Claims["sub"] = "" },
		"subject too long":            func(s *authtest.TokenSpec) { s.Claims["sub"] = strings.Repeat("1", 256) },
		"subject with a newline":      func(s *authtest.TokenSpec) { s.Claims["sub"] = "123\n456" },
		"no id_token in the response": func(s *authtest.TokenSpec) { s.OmitIDToken = true },
		"the token endpoint fails":    func(s *authtest.TokenSpec) { s.Status = http.StatusInternalServerError },
		"the token endpoint refuses":  func(s *authtest.TokenSpec) { s.Status = http.StatusBadRequest },
	}
	for name, tweak := range bad {
		t.Run(name, func(t *testing.T) {
			e.idp.OnToken(tweak)
			defer e.idp.OnToken(nil)
			f := e.browse("/", ana)
			wantFailed(t, f)
		})
	}

	good := map[string]struct {
		tweak func(*authtest.TokenSpec)
		want  Identity
	}{
		"untouched":                      {func(*authtest.TokenSpec) {}, Identity{Subject: ana.Sub, Name: "Ana García"}},
		"no azp with a single audience":  {func(s *authtest.TokenSpec) { delete(s.Claims, "azp") }, Identity{Subject: ana.Sub, Name: "Ana García"}},
		"audience as a list of one":      {func(s *authtest.TokenSpec) { s.Claims["aud"] = []string{testClientID} }, Identity{Subject: ana.Sub, Name: "Ana García"}},
		"two audiences, azp is us":       {func(s *authtest.TokenSpec) { s.Claims["aud"] = []string{"other", testClientID} }, Identity{Subject: ana.Sub, Name: "Ana García"}},
		"no name":                        {func(s *authtest.TokenSpec) { delete(s.Claims, "name") }, Identity{Subject: ana.Sub}},
		"a very long name is cut":        {func(s *authtest.TokenSpec) { s.Claims["name"] = "Maria de los Angeles Fernandez de Kirchner" }, Identity{Subject: ana.Sub, Name: "Maria de los Ang"}},
		"control characters are removed": {func(s *authtest.TokenSpec) { s.Claims["name"] = "A\x00n\u200Ba\x07 \t García" }, Identity{Subject: ana.Sub, Name: "Ana García"}},
		"a 255-character subject":        {func(s *authtest.TokenSpec) { s.Claims["sub"] = strings.Repeat("7", 255) }, Identity{Subject: strings.Repeat("7", 255), Name: "Ana García"}},
		"token expiring in a second":     {func(s *authtest.TokenSpec) { s.Claims["exp"] = now().Add(time.Second).Unix() }, Identity{Subject: ana.Sub, Name: "Ana García"}},
	}
	for name, tc := range good {
		t.Run("accepts "+name, func(t *testing.T) {
			e.idp.OnToken(tc.tweak)
			defer e.idp.OnToken(nil)
			f := e.browse("/", ana)
			if f.err != nil {
				t.Fatalf("Finish: %v", f.err)
			}
			if f.id != tc.want {
				t.Errorf("identity = %+v, want %+v", f.id, tc.want)
			}
		})
	}
}

func TestGoogleLogsNeverContainSecrets(t *testing.T) {
	e := newLoginEnv(t)
	var secrets []string
	record := func(s started, callback string) {
		tx, _ := e.g.parseTx(s.cookie.Value)
		u, _ := url.Parse(callback)
		secrets = append(secrets, tx.State, tx.Nonce, tx.Verifier, s.cookie.Value, u.Query().Get("code"))
	}
	for name, tweak := range map[string]func(*authtest.TokenSpec){
		"ok":      func(*authtest.TokenSpec) {},
		"aud":     func(s *authtest.TokenSpec) { s.Claims["aud"] = "x" },
		"nonce":   func(s *authtest.TokenSpec) { s.Claims["nonce"] = "x" },
		"sig":     func(s *authtest.TokenSpec) { s.CorruptSignature = true },
		"alg":     func(s *authtest.TokenSpec) { s.Header["alg"] = "none"; s.Unsigned = true },
		"expired": func(s *authtest.TokenSpec) { s.Claims["exp"] = e.clock.Now().Add(-time.Hour).Unix() },
		"500":     func(s *authtest.TokenSpec) { s.Status = 500 },
	} {
		e.idp.OnToken(tweak)
		s := e.start("/")
		cb, _ := e.idp.Authorize(s.authURL, ana)
		record(s, cb)
		e.finish(callbackRequest(cb, s.cookie))
		_ = name
	}
	e.idp.SetDown(true)
	s := e.start("/")
	_ = s

	logs := e.logs.String()
	if logs == "" {
		t.Fatal("nothing was logged: the test proves nothing")
	}
	secrets = append(secrets, testClientSecret, testSecret, "eyJ")
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("the logs contain a secret (%.8s...)", secret)
		}
	}
}

func TestGoogleTokenEndpointTimeout(t *testing.T) {
	e := newLoginEnv(t, func(c *GoogleConfig) { c.Timeout = 150 * time.Millisecond })
	e.idp.OnToken(func(s *authtest.TokenSpec) { s.Delay = 5 * time.Second })
	started := time.Now()
	f := e.browse("/", ana)
	wantFailed(t, f)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("a stalled token endpoint held the login for %v", elapsed)
	}
}

func TestGoogleDiscoveryIsLazyAndCached(t *testing.T) {
	e := newLoginEnv(t)
	e.idp.SetDown(true)
	// Constructing the flow and a failing start never need the provider to be up.
	s := e.start("/")
	if !errors.Is(s.err, ErrLoginFailed) || s.authURL != "" {
		t.Errorf("Start with the provider down = %q, %v", s.authURL, s.err)
	}
	if s.cookie != nil {
		t.Error("a failed Start set the transaction cookie")
	}

	e.idp.SetDown(false)
	if f := e.browse("/", ana); f.err != nil {
		t.Fatalf("the provider came up but login still fails: %v", f.err)
	}
	e.idp.SetDown(true)
	// The discovery document is cached: starting a login needs no network.
	if s := e.start("/"); s.err != nil {
		t.Errorf("Start with a cached discovery failed: %v", s.err)
	}
	e.idp.SetDown(false)
	for range 3 {
		if f := e.browse("/", ana); f.err != nil {
			t.Fatal(f.err)
		}
	}
	if n := e.idp.Hits("/.well-known/openid-configuration"); n != 2 {
		t.Errorf("discovery hits = %d, want 2 (one failure, one success, then cached)", n)
	}
	if n := e.idp.Hits("/jwks"); n != 1 {
		t.Errorf("jwks hits = %d, want 1 (keys are cached)", n)
	}
}

func TestGoogleProviderDownAtTheCallback(t *testing.T) {
	e := newLoginEnv(t)
	s := e.start("/")
	cb, _ := e.idp.Authorize(s.authURL, ana)
	e.idp.SetDown(true)
	wantFailed(t, e.finish(callbackRequest(cb, s.cookie)))
}

func TestGoogleDiscoveryNeverRunsInTheConstructor(t *testing.T) {
	idp := authtest.New(t, testClientID, testClientSecret)
	NewGoogle(GoogleConfig{ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testRedirectURL, Secret: []byte(testSecret), Issuer: idp.URL})
	if n := idp.Hits("/.well-known/openid-configuration"); n != 0 {
		t.Errorf("NewGoogle fetched the discovery document (%d hits): server start would depend on Google", n)
	}
}

func TestGoogleConcurrentFirstUse(t *testing.T) {
	e := newLoginEnv(t)
	e.idp.SetDown(true)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if s := e.start("/"); s.err == nil {
				t.Error("Start succeeded with the provider down")
			}
		})
	}
	wg.Wait()
	e.idp.SetDown(false)
	for range 10 {
		wg.Go(func() {
			s := e.start("/")
			if s.err != nil {
				t.Errorf("Start: %v", s.err)
				return
			}
			cb, err := e.idp.Authorize(s.authURL, ana)
			if err != nil {
				t.Error(err)
				return
			}
			if f := e.finish(callbackRequest(cb, s.cookie)); f.err != nil {
				t.Errorf("Finish: %v", f.err)
			}
		})
	}
	wg.Wait()
}

// stubProvider is a hand-written provider for the cases the fake one cannot
// express (oversized bodies, redirects).
func stubProvider(t *testing.T, serve func(w http.ResponseWriter, r *http.Request, base string)) string {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(w, r, srv.URL) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestGoogleRefusesOversizedProviderResponses(t *testing.T) {
	issuer := stubProvider(t, func(w http.ResponseWriter, r *http.Request, base string) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		doc := map[string]any{
			"issuer": base, "authorization_endpoint": base + "/auth", "token_endpoint": base + "/token",
			"jwks_uri": base + "/jwks", "padding": strings.Repeat("A", 2<<20),
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	g := NewGoogle(GoogleConfig{ClientID: "id", ClientSecret: "s", RedirectURL: testRedirectURL, Secret: []byte(testSecret), Issuer: issuer})
	rec := httptest.NewRecorder()
	if _, err := g.Start(context.Background(), rec, "/"); !errors.Is(err, ErrLoginFailed) {
		t.Errorf("Start with a 2 MiB discovery document: %v", err)
	}
}

func TestLimitedBody(t *testing.T) {
	read := func(n, limit int) (int, error) {
		b := &limitedBody{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", n))), left: int64(limit)}
		out, err := io.ReadAll(b)
		return len(out), err
	}
	if n, err := read(100, 100); err != nil || n != 100 {
		t.Errorf("a body exactly at the limit: %d, %v", n, err)
	}
	if n, err := read(101, 100); !errors.Is(err, errBodyTooLarge) || n > 101 {
		t.Errorf("a body one byte over the limit: %d, %v", n, err)
	}
	if _, err := read(10_000, 100); !errors.Is(err, errBodyTooLarge) {
		t.Errorf("a body far over the limit: %v", err)
	}
	if n, err := read(0, 100); err != nil || n != 0 {
		t.Errorf("an empty body: %d, %v", n, err)
	}
}

func TestGoogleDoesNotFollowRedirects(t *testing.T) {
	var evilHits int
	var mu sync.Mutex
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		evilHits++
		mu.Unlock()
	}))
	t.Cleanup(evil.Close)
	issuer := stubProvider(t, func(w http.ResponseWriter, r *http.Request, base string) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": base, "authorization_endpoint": base + "/auth", "token_endpoint": base + "/token",
				"jwks_uri": base + "/jwks",
			})
		case "/token":
			http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect)
		default:
			http.NotFound(w, r)
		}
	})
	e := &loginEnv{t: t, clock: &testClock{t: time.Now()}, logs: &syncBuffer{}}
	e.g = NewGoogle(GoogleConfig{ClientID: "id", ClientSecret: "s", RedirectURL: testRedirectURL, Secret: []byte(testSecret), Issuer: issuer, Now: e.clock.Now})

	s := e.start("/")
	if s.err != nil {
		t.Fatal(s.err)
	}
	u, _ := url.Parse(s.authURL)
	cb := testRedirectURL + "?" + url.Values{"code": {"abc"}, "state": {u.Query().Get("state")}}.Encode()
	wantFailed(t, e.finish(callbackRequest(cb, s.cookie)))
	mu.Lock()
	defer mu.Unlock()
	if evilHits != 0 {
		t.Errorf("the token request (with the client secret) was replayed to a redirect target %d times", evilHits)
	}
}

func TestCleanDisplayName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Ana", "Ana"},
		{"  Ana   García  ", "Ana García"},
		{"", ""},
		{"   ", ""},
		{"A\x00B", "AB"},
		{"A\u200bB", "AB"},
		{"A\u202eB", "AB"},
		{"A\nB\tC", "A B C"},
		{"\x01\x02", ""},
		{"Ñandú 🍉", "Ñandú 🍉"},
		{"1234567890123456", "1234567890123456"},
		{"12345678901234567", "1234567890123456"},
		{"Maria de los Angeles", "Maria de los Ang"},
		{"Maria de los Angeles", "Maria de los Ang"},
		{"123456789012345 67", "123456789012345"}, // cut right after a space: trimmed
		{"🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉", "🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉🍉"},
		{"ok\xffbad", "okbad"},
	}
	for _, tc := range tests {
		got := CleanDisplayName(tc.in)
		if got != tc.want {
			t.Errorf("CleanDisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if len([]rune(got)) > MaxDisplayNameRunes {
			t.Errorf("CleanDisplayName(%q) has %d runes", tc.in, len([]rune(got)))
		}
	}
}

func TestNewGooglePanicsWithoutRequiredFields(t *testing.T) {
	full := GoogleConfig{ClientID: "id", ClientSecret: "s", RedirectURL: "http://x/cb", Secret: []byte("secret")}
	NewGoogle(full) // fine
	for name, mutate := range map[string]func(*GoogleConfig){
		"client id":     func(c *GoogleConfig) { c.ClientID = "" },
		"client secret": func(c *GoogleConfig) { c.ClientSecret = "" },
		"redirect url":  func(c *GoogleConfig) { c.RedirectURL = "" },
		"secret":        func(c *GoogleConfig) { c.Secret = nil },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("no panic without %s", name)
				}
			}()
			c := full
			mutate(&c)
			NewGoogle(c)
		}()
	}
}

func TestErrDetail(t *testing.T) {
	if got := errDetail(errors.New(strings.Repeat("é", 300))); len(got) > 210 {
		t.Errorf("errDetail is %d bytes long", len(got))
	}
	if got := errDetail(&oauthRetrieveForTest); !strings.Contains(got, "400") || !strings.Contains(got, "invalid_grant") || strings.Contains(got, "SECRET") {
		t.Errorf("errDetail(RetrieveError) = %q", got)
	}
}

var oauthRetrieveForTest = oauth2.RetrieveError{
	Response:         &http.Response{StatusCode: 400},
	Body:             []byte(`{"error":"invalid_grant","SECRET":"x"}`),
	ErrorCode:        "invalid_grant",
	ErrorDescription: "SECRET",
}
