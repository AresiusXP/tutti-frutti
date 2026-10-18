package auth

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// issue mints a cookie with g and returns its Set-Cookie and the ID.
func issue(t *testing.T, g *Guests) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	id := g.Issue(rec)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Issue set %d cookies, want 1", len(cookies))
	}
	return cookies[0], id
}

func reqWith(cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func TestRoundTrip(t *testing.T) {
	g := NewGuests([]byte("secret"), false, discard)
	c, id := issue(t, g)

	got, ok := g.Identify(reqWith(c))
	if !ok || got != id {
		t.Fatalf("Identify = %q, %v; want %q, true", got, ok, id)
	}
	if len(id) != 22 {
		t.Errorf("id %q has length %d, want 22 (16 bytes base64url)", id, len(id))
	}
	if !strings.HasPrefix(c.Value, id+".") {
		t.Errorf("cookie value %q does not start with <id>.", c.Value)
	}
}

func TestCookieAttributes(t *testing.T) {
	tests := []struct {
		name   string
		secure bool
	}{
		{"insecure", false},
		{"secure", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGuests([]byte("secret"), tt.secure, discard)
			c, _ := issue(t, g)
			if c.Name != protocol.GuestCookie {
				t.Errorf("Name = %q, want %q", c.Name, protocol.GuestCookie)
			}
			if !c.HttpOnly {
				t.Error("cookie must be HttpOnly")
			}
			if c.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", c.Secure, tt.secure)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
			if c.Path != "/" {
				t.Errorf("Path = %q, want /", c.Path)
			}
			if c.MaxAge != 365*24*3600 {
				t.Errorf("MaxAge = %d, want one year", c.MaxAge)
			}
		})
	}
}

func TestIdentifyRejects(t *testing.T) {
	g := NewGuests([]byte("secret"), false, discard)
	c, _ := issue(t, g)
	id, mac, _ := strings.Cut(c.Value, ".")

	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	other := NewGuests([]byte("other secret"), false, discard)
	foreign, _ := issue(t, other)

	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"no dot", id + mac},
		{"only dot", "."},
		{"tampered id", flip(id, 3) + "." + mac},
		{"tampered mac", id + "." + flip(mac, 3)},
		{"tampered last mac char", id + "." + flip(mac, len(mac)-1)},
		{"swapped parts", mac + "." + id},
		{"truncated mac", c.Value[:len(c.Value)-1]},
		{"truncated half", c.Value[:len(c.Value)/2]},
		{"truncated to id", id},
		{"id with empty mac", id + "."},
		{"empty id with mac", "." + mac},
		{"extra dot", c.Value + "."},
		{"extra part", c.Value + ".x"},
		{"oversized", strings.Repeat("A", 1<<16)},
		{"oversized valid prefix", c.Value + strings.Repeat("A", 4096)},
		{"not base64", strings.Repeat("!", 22) + "." + strings.Repeat("!", 43)},
		{"non ascii", strings.Repeat("é", 11) + "." + strings.Repeat("é", 21) + "x"},
		{"padding chars", id + "." + mac[:len(mac)-1] + "="},
		{"wrong secret", foreign.Value},
		{"unsigned id", id + "." + strings.Repeat("A", 43)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reqWith(&http.Cookie{Name: protocol.GuestCookie, Value: tt.value})
			if got, ok := g.Identify(r); ok {
				t.Errorf("Identify accepted %q as %q", tt.value, got)
			}
		})
	}

	t.Run("no cookie", func(t *testing.T) {
		if _, ok := g.Identify(reqWith()); ok {
			t.Error("Identify accepted a request without cookie")
		}
	})
	t.Run("other cookie name", func(t *testing.T) {
		r := reqWith(&http.Cookie{Name: "other", Value: c.Value})
		if _, ok := g.Identify(r); ok {
			t.Error("Identify accepted a cookie with the wrong name")
		}
	})
}

func TestIdentifyEveryLastCharVariantOfTheMAC(t *testing.T) {
	// 43 base64 chars encode 256 bits plus 2 padding bits: only the canonical
	// spelling of the MAC may verify, never a variant of its last character.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	g := NewGuests([]byte("secret"), false, discard)
	for i := 0; i < 20; i++ {
		c, _ := issue(t, g)
		prefix := c.Value[:len(c.Value)-1]
		accepted := 0
		for _, ch := range alphabet {
			v := &http.Cookie{Name: protocol.GuestCookie, Value: prefix + string(ch)}
			if _, ok := g.Identify(reqWith(v)); ok {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("%d spellings of one cookie were accepted, want exactly 1 (%s)", accepted, c.Value)
		}
	}
}

func TestIdentifyNonCanonicalID(t *testing.T) {
	// A correctly signed id whose trailing padding bits are not zero decodes to
	// the same 16 bytes as the canonical one; it must be refused so one ID
	// cannot have several string forms.
	g := NewGuests([]byte("secret"), false, discard)
	c, _ := issue(t, g)
	id, _, _ := strings.Cut(c.Value, ".")
	alt := id[:len(id)-1] + string(nextBase64(id[len(id)-1]))
	if raw, err := enc.DecodeString(alt); err != nil || enc.EncodeToString(raw) == alt {
		t.Fatalf("test setup: %q should be a non-canonical but decodable id", alt)
	}
	forged := alt + "." + enc.EncodeToString(g.mac(alt))
	if got, ok := g.Identify(reqWith(&http.Cookie{Name: protocol.GuestCookie, Value: forged})); ok {
		t.Errorf("non-canonical id accepted as %q", got)
	}
}

func nextBase64(c byte) byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, c)
	return alphabet[(i+1)%len(alphabet)]
}

func TestWrongSecret(t *testing.T) {
	a := NewGuests([]byte("secret-a"), false, discard)
	b := NewGuests([]byte("secret-b"), false, discard)
	c, _ := issue(t, a)
	if _, ok := b.Identify(reqWith(c)); ok {
		t.Error("a cookie signed with another secret was accepted")
	}
	if _, ok := a.Identify(reqWith(c)); !ok {
		t.Error("the cookie must still verify with its own secret")
	}
}

func TestSameSecretAcrossInstances(t *testing.T) {
	a := NewGuests([]byte("shared"), false, discard)
	b := NewGuests([]byte("shared"), true, discard)
	c, id := issue(t, a)
	if got, ok := b.Identify(reqWith(c)); !ok || got != id {
		t.Errorf("a restart with the same secret must keep sessions: %q %v", got, ok)
	}
}

func TestSecretIsCopied(t *testing.T) {
	secret := []byte("mutable secret")
	g := NewGuests(secret, false, discard)
	c, _ := issue(t, g)
	for i := range secret {
		secret[i] = 0
	}
	if _, ok := g.Identify(reqWith(c)); !ok {
		t.Error("mutating the caller's slice changed the signing secret")
	}
}

func TestEmptySecretGeneratesEphemeral(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	a := NewGuests(nil, false, log)
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "will not survive a restart") {
		t.Errorf("missing warning about ephemeral secret, got %q", buf.String())
	}
	if len(a.secret) != 32 {
		t.Errorf("generated secret has %d bytes, want 32", len(a.secret))
	}
	c, id := issue(t, a)
	if got, ok := a.Identify(reqWith(c)); !ok || got != id {
		t.Errorf("ephemeral secret must verify its own cookies: %q %v", got, ok)
	}

	b := NewGuests([]byte{}, false, discard)
	if bytes.Equal(a.secret, b.secret) {
		t.Error("two generated secrets are identical")
	}
	if _, ok := b.Identify(reqWith(c)); ok {
		t.Error("cookies must not verify across ephemeral secrets")
	}
}

func TestWeakSecretWarns(t *testing.T) {
	tests := []struct {
		name     string
		secret   string
		wantWarn bool
	}{
		{"short", "hunter2", true},
		{"31 bytes", strings.Repeat("k", 31), true},
		{"32 bytes", strings.Repeat("k", 32), false},
		{"long", strings.Repeat("k", 64), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			g := NewGuests([]byte(tt.secret), false, slog.New(slog.NewTextHandler(&buf, nil)))
			warned := strings.Contains(buf.String(), "level=WARN")
			if warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v (log: %q)", warned, tt.wantWarn, buf.String())
			}
			if strings.Contains(buf.String(), tt.secret) {
				t.Errorf("the secret was logged: %q", buf.String())
			}
			// A short secret keeps working: only config.Load refuses it.
			c, id := issue(t, g)
			if got, ok := g.Identify(reqWith(c)); !ok || got != id {
				t.Errorf("round trip failed: %q %v", got, ok)
			}
		})
	}
}

func TestNewGuestsNilLoggerUsesDefault(t *testing.T) {
	if g := NewGuests([]byte("x"), false, nil); g == nil {
		t.Fatal("NewGuests returned nil")
	}
}

func TestIssueUniqueIDs(t *testing.T) {
	g := NewGuests([]byte("secret"), false, discard)
	seen := make(map[string]bool)
	for i := 0; i < 2000; i++ {
		_, id := issue(t, g)
		if seen[id] {
			t.Fatalf("duplicate id %q after %d issues", id, i)
		}
		seen[id] = true
	}
}

func TestEnsure(t *testing.T) {
	g := NewGuests([]byte("secret"), false, discard)

	t.Run("issues when absent", func(t *testing.T) {
		rec := httptest.NewRecorder()
		id := g.Ensure(rec, reqWith())
		cookies := rec.Result().Cookies()
		if id == "" || len(cookies) != 1 {
			t.Fatalf("Ensure = %q with %d cookies", id, len(cookies))
		}
		if got, ok := g.Identify(reqWith(cookies[0])); !ok || got != id {
			t.Errorf("issued cookie does not identify %q: %q %v", id, got, ok)
		}
	})
	t.Run("reuses a valid cookie", func(t *testing.T) {
		c, id := issue(t, g)
		rec := httptest.NewRecorder()
		if got := g.Ensure(rec, reqWith(c)); got != id {
			t.Errorf("Ensure = %q, want %q", got, id)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Error("Ensure must not re-issue a valid cookie")
		}
	})
	t.Run("replaces a forged cookie", func(t *testing.T) {
		rec := httptest.NewRecorder()
		forged := &http.Cookie{Name: protocol.GuestCookie, Value: strings.Repeat("A", 22) + "." + strings.Repeat("A", 43)}
		id := g.Ensure(rec, reqWith(forged))
		if id == strings.Repeat("A", 22) {
			t.Error("Ensure trusted a forged ID")
		}
		if len(rec.Result().Cookies()) != 1 {
			t.Error("Ensure must mint a fresh cookie for a forged one")
		}
	})
}

func TestIdentifyAnyOfSeveralCookies(t *testing.T) {
	g := NewGuests([]byte("secret"), false, discard)
	good, id := issue(t, g)
	bad := &http.Cookie{Name: protocol.GuestCookie, Value: "garbage"}
	if got, ok := g.Identify(reqWith(bad, good)); !ok || got != id {
		t.Errorf("Identify = %q %v, want the valid cookie to win", got, ok)
	}
}

func TestConcurrentUse(t *testing.T) {
	g := NewGuests([]byte("secret"), true, discard)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rec := httptest.NewRecorder()
				id := g.Issue(rec)
				c := rec.Result().Cookies()[0]
				if got, ok := g.Identify(reqWith(c)); !ok || got != id {
					t.Errorf("round trip failed: %q %v", got, ok)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func FuzzIdentify(f *testing.F) {
	g := NewGuests([]byte("secret"), false, discard)
	rec := httptest.NewRecorder()
	g.Issue(rec)
	f.Add(rec.Result().Cookies()[0].Value)
	f.Add("")
	f.Add(".")
	f.Add(strings.Repeat("A", 22) + "." + strings.Repeat("A", 43))
	f.Fuzz(func(t *testing.T, value string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Cookie", protocol.GuestCookie+"="+value)
		_, _ = g.Identify(r) // must not panic
	})
}
