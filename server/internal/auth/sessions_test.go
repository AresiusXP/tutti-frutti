package auth

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

var testNow = time.Date(2026, time.March, 14, 9, 26, 53, 0, time.UTC)

const testSecret = "a-test-secret-that-is-long-enough-0123456789"

func sessionAt(now time.Time, secure bool) *Sessions {
	return NewSessions([]byte(testSecret), secure, WithSessionClock(func() time.Time { return now }))
}

// sessionReq is a request carrying value as its tf_session cookie, verbatim
// (AddCookie would silently sanitise it).
func sessionReq(value string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", SessionCookie+"="+value)
	return r
}

// issueSession mints a session cookie for a fresh user id.
func issueSession(t *testing.T, s *Sessions) (*http.Cookie, string) {
	t.Helper()
	id := store.NewID()
	rec := httptest.NewRecorder()
	if err := s.Issue(rec, id); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Issue set %d cookies, want 1", len(cookies))
	}
	return cookies[0], id
}

// forge signs a session value with key, whatever it says.
func forge(key []byte, id string, expiry int64) string {
	msg := id + "." + strconv.FormatInt(expiry, 10)
	return msg + "." + enc.EncodeToString(signMAC(key, msg))
}

func TestSessionRoundTrip(t *testing.T) {
	s := sessionAt(testNow, false)
	c, id := issueSession(t, s)
	got, ok := s.Identify(sessionReq(c.Value))
	if !ok || got != id {
		t.Fatalf("Identify = %q, %v; want %q, true", got, ok, id)
	}

	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 || parts[0] != id {
		t.Fatalf("value %q is not <userId>.<expiry>.<mac>", c.Value)
	}
	if want := strconv.FormatInt(testNow.Add(30*24*time.Hour).Unix(), 10); parts[1] != want {
		t.Errorf("expiry = %s, want %s (30 days)", parts[1], want)
	}
	if len(parts[2]) != macLen {
		t.Errorf("mac has %d characters, want %d", len(parts[2]), macLen)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			c, _ := issueSession(t, sessionAt(testNow, secure))
			if c.Name != "tf_session" || c.Path != "/" || c.MaxAge != 30*24*60*60 {
				t.Errorf("cookie = %+v", c)
			}
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != secure {
				t.Errorf("HttpOnly=%v SameSite=%v Secure=%v", c.HttpOnly, c.SameSite, c.Secure)
			}
		})
	}
}

func TestSessionClear(t *testing.T) {
	rec := httptest.NewRecorder()
	sessionAt(testNow, true).Clear(rec)
	h := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"tf_session=;", "Max-Age=0", "Path=/", "HttpOnly", "SameSite=Lax", "Secure"} {
		if !strings.Contains(h, want) {
			t.Errorf("Set-Cookie %q lacks %q", h, want)
		}
	}
}

func TestSessionIssueRefusesAMalformedUserID(t *testing.T) {
	s := sessionAt(testNow, false)
	for _, id := range []string{"", "x", "guest", strings.ToUpper(store.NewID()), store.NewID() + "0", "a.b"} {
		rec := httptest.NewRecorder()
		if err := s.Issue(rec, id); err == nil {
			t.Errorf("Issue(%q) succeeded", id)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("Issue(%q) set a cookie although it failed", id)
		}
	}
}

func TestSessionExpiry(t *testing.T) {
	issued := sessionAt(testNow, false)
	c, id := issueSession(t, issued)
	exp := testNow.Add(SessionMaxAge)
	for _, tc := range []struct {
		name string
		at   time.Time
		ok   bool
	}{
		{"just issued", testNow, true},
		{"after 29 days", testNow.Add(29 * 24 * time.Hour), true},
		{"one second before the expiry", exp.Add(-time.Second), true},
		{"at the expiry", exp, false},
		{"one second after", exp.Add(time.Second), false},
		{"a year later", testNow.Add(365 * 24 * time.Hour), false},
	} {
		got, ok := sessionAt(tc.at, false).Identify(sessionReq(c.Value))
		if ok != tc.ok || (ok && got != id) {
			t.Errorf("%s: Identify = %q, %v; want ok=%v", tc.name, got, ok, tc.ok)
		}
	}
}

func TestSessionRefusesAnExpiryTooFarAhead(t *testing.T) {
	// Signed by us but a year ahead: not something Issue ever produces.
	s := sessionAt(testNow, false)
	key := deriveKey([]byte(testSecret), sessionKeyLabel)
	id := store.NewID()
	okExp := testNow.Add(SessionMaxAge).Unix()
	if _, ok := s.Identify(sessionReq(forge(key, id, okExp))); !ok {
		t.Fatal("a correctly signed session was refused")
	}
	if _, ok := s.Identify(sessionReq(forge(key, id, okExp+int64(maxExpirySkew/time.Second)))); !ok {
		t.Error("an expiry within the allowed skew was refused")
	}
	if _, ok := s.Identify(sessionReq(forge(key, id, okExp+61))); ok {
		t.Error("an expiry more than 30 days and a minute ahead was accepted")
	}
	if _, ok := s.Identify(sessionReq(forge(key, id, testNow.Add(365*24*time.Hour).Unix()))); ok {
		t.Error("an expiry a year ahead was accepted")
	}
	if _, ok := s.Identify(sessionReq(forge(key, id, testNow.Unix()))); ok {
		t.Error("an expiry of right now was accepted")
	}
}

func TestSessionRejects(t *testing.T) {
	s := sessionAt(testNow, false)
	c, id := issueSession(t, s)
	parts := strings.Split(c.Value, ".")
	other := store.NewID()
	key := deriveKey([]byte(testSecret), sessionKeyLabel)
	exp := testNow.Add(time.Hour).Unix()

	cases := map[string]string{
		"empty":                    "",
		"dot":                      ".",
		"two dots":                 "..",
		"no mac":                   parts[0] + "." + parts[1],
		"empty mac":                parts[0] + "." + parts[1] + ".",
		"empty expiry":             forgeRaw(key, id, ""),
		"extra segment":            c.Value + ".x",
		"another user's id":        other + "." + parts[1] + "." + parts[2],
		"extended expiry":          parts[0] + "." + strconv.FormatInt(testNow.Add(2*SessionMaxAge).Unix(), 10) + "." + parts[2],
		"shortened expiry":         parts[0] + "." + strconv.FormatInt(testNow.Add(time.Hour).Unix(), 10) + "." + parts[2],
		"mac of another message":   parts[0] + "." + parts[1] + "." + enc.EncodeToString(signMAC(key, other+"."+parts[1])),
		"upper-case id":            strings.ToUpper(parts[0]) + "." + parts[1] + "." + parts[2],
		"leading zero in expiry":   forgeRaw(key, id, "0"+strconv.FormatInt(exp, 10)),
		"plus sign in expiry":      forgeRaw(key, id, "+"+strconv.FormatInt(exp, 10)),
		"hex expiry":               forgeRaw(key, id, "0x"+strconv.FormatInt(exp, 16)),
		"space in expiry":          forgeRaw(key, id, " "+strconv.FormatInt(exp, 10)),
		"negative expiry":          forgeRaw(key, id, "-1"),
		"zero expiry":              forgeRaw(key, id, "0"),
		"12-digit expiry":          forgeRaw(key, id, "999999999999"),
		"expiry with a fraction":   forgeRaw(key, id, strconv.FormatInt(exp, 10)+".5"),
		"id with a different dash": strings.Replace(parts[0], "-", "_", 1) + "." + parts[1] + "." + parts[2],
		"mac with padding":         parts[0] + "." + parts[1] + "." + parts[2] + "=",
		"mac not base64":           parts[0] + "." + parts[1] + "." + strings.Repeat("*", macLen),
		"signed but not a uuid":    forgeRaw(key, strings.Repeat("Z", 36), strconv.FormatInt(exp, 10)),
		"signed, uuid in capitals": forgeRaw(key, strings.ToUpper(id), strconv.FormatInt(exp, 10)),
		"oversized":                c.Value + strings.Repeat("A", 10_000),
		"huge garbage":             strings.Repeat("a.", 50_000),
	}
	for name, v := range cases {
		if got, ok := s.Identify(sessionReq(v)); ok {
			t.Errorf("%s: accepted as user %q", name, got)
		}
	}
	// The untampered cookie is fine: the table above fails for the right reasons.
	if _, ok := s.Identify(sessionReq(c.Value)); !ok {
		t.Fatal("the original cookie is refused")
	}
}

// forgeRaw signs id + "." + expiry as given (which may not be a number).
func forgeRaw(key []byte, id, expiry string) string {
	msg := id + "." + expiry
	return msg + "." + enc.EncodeToString(signMAC(key, msg))
}

func TestSessionTruncation(t *testing.T) {
	s := sessionAt(testNow, false)
	c, _ := issueSession(t, s)
	for n := range len(c.Value) {
		if _, ok := s.Identify(sessionReq(c.Value[:n])); ok {
			t.Errorf("a value truncated to %d of %d characters was accepted", n, len(c.Value))
		}
	}
}

func TestSessionEveryLastCharVariantOfTheMAC(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	s := sessionAt(testNow, false)
	for range 10 {
		c, _ := issueSession(t, s)
		prefix := c.Value[:len(c.Value)-1]
		accepted := 0
		for _, ch := range alphabet {
			if _, ok := s.Identify(sessionReq(prefix + string(ch))); ok {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("%d spellings of one session were accepted, want exactly 1", accepted)
		}
	}
}

func TestSessionEveryFlippedBitIsRefused(t *testing.T) {
	s := sessionAt(testNow, false)
	c, _ := issueSession(t, s)
	for i := range len(c.Value) {
		for bit := range 7 {
			b := []byte(c.Value)
			b[i] ^= 1 << bit
			if !cookieSafe(b[i]) {
				continue // the HTTP parser would drop such a cookie before we see it
			}
			if _, ok := s.Identify(sessionReq(string(b))); ok {
				t.Fatalf("flipping bit %d of byte %d still verifies: %q", bit, i, b)
			}
		}
	}
}

func cookieSafe(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

func TestSessionWrongSecret(t *testing.T) {
	c, _ := issueSession(t, sessionAt(testNow, false))
	other := NewSessions([]byte("another-secret-that-is-long-enough-xyz"), false, WithSessionClock(func() time.Time { return testNow }))
	if _, ok := other.Identify(sessionReq(c.Value)); ok {
		t.Error("a session verified under another secret")
	}
	same := NewSessions([]byte(testSecret), false, WithSessionClock(func() time.Time { return testNow }))
	if _, ok := same.Identify(sessionReq(c.Value)); !ok {
		t.Error("a session does not verify under the same secret in another instance (restart)")
	}
}

func TestSessionSecretIsCopiedByNewSecret(t *testing.T) {
	raw := []byte(testSecret)
	secret := NewSecret(raw, discard)
	raw[0] ^= 0xff
	if string(secret) != testSecret {
		t.Error("NewSecret kept a reference to its argument")
	}
}

func TestSessionsAnyOfSeveralCookies(t *testing.T) {
	s := sessionAt(testNow, false)
	c, id := issueSession(t, s)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "garbage"})
	r.AddCookie(c)
	if got, ok := s.Identify(r); !ok || got != id {
		t.Errorf("Identify with a bad and a good cookie = %q, %v", got, ok)
	}
	if _, ok := s.Identify(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("a request without cookies is logged in")
	}
}

// TestCookieDomainSeparation: the guest cookie, the session cookie and the
// OAuth transaction cookie are signed with different keys derived from the
// same secret: one can never be replayed as another, not even by a client that
// renames it.
func TestCookieDomainSeparation(t *testing.T) {
	secret := []byte(testSecret)
	now := func() time.Time { return testNow }
	guests := NewGuests(secret, false, discard)
	sessions := NewSessions(secret, false, WithSessionClock(now))
	google := NewGoogle(GoogleConfig{ClientID: "id", ClientSecret: "s", RedirectURL: "http://x/cb", Secret: secret, Now: now})

	guestCookie, _ := issue(t, guests)
	sessionCookie, userID := issueSession(t, sessions)
	tx := oauthTx{
		State: randomToken(), Verifier: randomToken(), Nonce: randomToken(), ReturnTo: "/",
		Expires: testNow.Add(OAuthTTL).Unix(),
	}
	txCookie := google.signTx(tx)
	if _, ok := google.parseTx(txCookie); !ok {
		t.Fatal("a freshly signed transaction does not parse")
	}

	as := func(name, value string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: value})
		return r
	}
	t.Run("a guest cookie is not a session", func(t *testing.T) {
		if _, ok := sessions.Identify(as(SessionCookie, guestCookie.Value)); ok {
			t.Error("the guest cookie value verified as a session")
		}
	})
	t.Run("a session is not a guest cookie", func(t *testing.T) {
		if _, ok := guests.Identify(as(protocol.GuestCookie, sessionCookie.Value)); ok {
			t.Error("the session value verified as a guest cookie")
		}
	})
	t.Run("a MAC made with the guest key does not sign a session", func(t *testing.T) {
		if _, ok := sessions.Identify(sessionReq(forge(secret, userID, testNow.Add(time.Hour).Unix()))); ok {
			t.Error("a session signed with the raw secret (the guest key) verified")
		}
	})
	t.Run("a MAC made with the OAuth key does not sign a session", func(t *testing.T) {
		key := deriveKey(secret, oauthKeyLabel)
		if _, ok := sessions.Identify(sessionReq(forge(key, userID, testNow.Add(time.Hour).Unix()))); ok {
			t.Error("a session signed with the OAuth key verified")
		}
	})
	t.Run("a session MAC does not sign an OAuth transaction", func(t *testing.T) {
		payload := txCookie[:strings.Index(txCookie, ".")]
		forged := payload + "." + enc.EncodeToString(signMAC(deriveKey(secret, sessionKeyLabel), payload))
		if _, ok := google.parseTx(forged); ok {
			t.Error("a transaction signed with the session key verified")
		}
		raw := payload + "." + enc.EncodeToString(signMAC(secret, payload))
		if _, ok := google.parseTx(raw); ok {
			t.Error("a transaction signed with the raw secret verified")
		}
	})
	t.Run("an OAuth transaction is not a session", func(t *testing.T) {
		if _, ok := sessions.Identify(as(SessionCookie, txCookie)); ok {
			t.Error("the transaction cookie verified as a session")
		}
	})
	t.Run("a guest cookie is not a transaction", func(t *testing.T) {
		if _, ok := google.parseTx(guestCookie.Value); ok {
			t.Error("the guest cookie parsed as a transaction")
		}
	})
	t.Run("the derived keys differ", func(t *testing.T) {
		keys := [][]byte{secret, deriveKey(secret, sessionKeyLabel), deriveKey(secret, oauthKeyLabel)}
		for i := range keys {
			for j := i + 1; j < len(keys); j++ {
				if bytes.Equal(keys[i], keys[j]) {
					t.Errorf("keys %d and %d are equal", i, j)
				}
			}
		}
	})
}

func TestNewSecret(t *testing.T) {
	t.Run("empty makes a random ephemeral secret and warns", func(t *testing.T) {
		var logs bytes.Buffer
		a := NewSecret(nil, slog.New(slog.NewTextHandler(&logs, nil)))
		b := NewSecret([]byte{}, discard)
		if len(a) != 32 || bytes.Equal(a, b) {
			t.Errorf("secrets %x and %x are not 32 distinct random bytes", a, b)
		}
		if !strings.Contains(logs.String(), "SESSION_SECRET is empty") {
			t.Errorf("no warning: %q", logs.String())
		}
	})
	t.Run("short is used and warned about without leaking it", func(t *testing.T) {
		var logs bytes.Buffer
		got := NewSecret([]byte("short-secret"), slog.New(slog.NewTextHandler(&logs, nil)))
		if string(got) != "short-secret" {
			t.Errorf("secret = %q", got)
		}
		if !strings.Contains(logs.String(), "shorter than the recommended minimum") || strings.Contains(logs.String(), "short-secret") {
			t.Errorf("log = %q", logs.String())
		}
	})
	t.Run("long is silent", func(t *testing.T) {
		var logs bytes.Buffer
		NewSecret([]byte(testSecret), slog.New(slog.NewTextHandler(&logs, nil)))
		if logs.Len() != 0 {
			t.Errorf("log = %q", logs.String())
		}
	})
	t.Run("nil logger uses the default", func(t *testing.T) {
		NewSecret([]byte(testSecret), nil)
	})
}

func TestNewSessionsPanicsWithoutASecret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewSessions(nil) did not panic")
		}
	}()
	NewSessions(nil, false)
}

func TestSessionConcurrentUse(t *testing.T) {
	s := sessionAt(testNow, false)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 50 {
				c, id := issueSession(t, s)
				if got, ok := s.Identify(sessionReq(c.Value)); !ok || got != id {
					t.Errorf("Identify = %q, %v", got, ok)
					return
				}
			}
		})
	}
	wg.Wait()
}

func FuzzSessionIdentify(f *testing.F) {
	s := sessionAt(testNow, false)
	rec := httptest.NewRecorder()
	_ = s.Issue(rec, store.NewID())
	f.Add(rec.Result().Cookies()[0].Value)
	f.Add("")
	f.Add("..")
	f.Add(store.NewID() + ".1." + strings.Repeat("A", macLen))
	f.Fuzz(func(t *testing.T, value string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Cookie", SessionCookie+"="+value)
		if id, ok := s.Identify(r); ok && !store.ValidID(id) {
			t.Errorf("Identify returned %q, not a user id", id)
		}
	})
}
