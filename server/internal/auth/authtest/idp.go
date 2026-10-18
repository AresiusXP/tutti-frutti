// Package authtest is a fake OpenID Connect identity provider for tests: an
// httptest server with a generated RSA key, a discovery document, a key set and
// an authorization-code token endpoint that checks the client secret and the
// PKCE verifier, and signs ID tokens that tests can tamper with. It is only
// imported by tests; nothing of it ends up in the server binary.
package authtest

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// KeyID is the "kid" of the provider's signing key.
const KeyID = "fake-key-1"

var b64 = base64.RawURLEncoding

// User is who logs in at the provider.
type User struct {
	Sub  string
	Name string
}

// TokenSpec is an ID token (and the token response around it) about to be
// issued. The provider fills in valid defaults and calls the hook installed
// with OnToken, which may change anything: that is how tests build a token
// with a wrong audience, an expired one, a bad signature...
type TokenSpec struct {
	// Header and Claims are the JOSE header and the payload.
	Header map[string]any
	Claims map[string]any
	// SignWith signs with another key than the provider's (nil: its own).
	SignWith *rsa.PrivateKey
	// HMACKey signs with HMAC-SHA256 when Header["alg"] is "HS256" (the classic
	// algorithm confusion attack uses the public key as that key).
	HMACKey []byte
	// Unsigned leaves the signature empty (use with Header["alg"] = "none").
	Unsigned bool
	// CorruptSignature flips a bit of the signature.
	CorruptSignature bool
	// OmitIDToken answers a valid token response without "id_token".
	OmitIDToken bool
	// Status answers the token request with this status and an OAuth error
	// body instead of tokens (0 = success).
	Status int
	// Delay holds the token response back (client timeout tests).
	Delay time.Duration
}

type grant struct {
	clientID    string
	redirectURI string
	challenge   string
	nonce       string
	user        User
	used        bool
}

// IdP is the fake provider. Create it with New.
type IdP struct {
	URL          string
	ClientID     string
	ClientSecret string

	srv *httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	now      func() time.Time
	down     bool
	hits     map[string]int
	grants   map[string]*grant
	lastAuth url.Values
	onToken  func(*TokenSpec)
	// lastToken is the last token request form (never the secret).
	lastToken url.Values
}

// New starts a provider for one client. The server is closed when the test
// ends.
func New(t testing.TB, clientID, clientSecret string) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &IdP{
		ClientID: clientID, ClientSecret: clientSecret, now: time.Now,
		key: key, hits: map[string]int{}, grants: map[string]*grant{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.wrap(p.discovery))
	mux.HandleFunc("GET /jwks", p.wrap(p.jwks))
	mux.HandleFunc("POST /token", p.wrap(p.token))
	p.srv = httptest.NewServer(mux)
	p.URL = p.srv.URL
	t.Cleanup(p.srv.Close)
	return p
}

// PrivateKey is the signing key (to forge tokens that verify).
func (p *IdP) PrivateKey() *rsa.PrivateKey { return p.key }

// SetClock sets the clock that stamps iat and exp of the tokens (default
// time.Now).
func (p *IdP) SetClock(now func() time.Time) {
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

// SetDown makes the provider unreachable (the connection is dropped) or
// reachable again.
func (p *IdP) SetDown(down bool) {
	p.mu.Lock()
	p.down = down
	p.mu.Unlock()
}

// Hits counts the requests the provider received on a path ("/token", ...).
func (p *IdP) Hits(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits[path]
}

// OnToken installs a hook that may change every token about to be issued
// (nil removes it).
func (p *IdP) OnToken(f func(*TokenSpec)) {
	p.mu.Lock()
	p.onToken = f
	p.mu.Unlock()
}

// LastAuthRequest returns the query of the last authorization request.
func (p *IdP) LastAuthRequest() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAuth
}

// LastTokenRequest returns the form of the last token request.
func (p *IdP) LastTokenRequest() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastToken
}

func (p *IdP) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits[r.URL.Path]++
		down := p.down
		p.mu.Unlock()
		if down {
			panic(http.ErrAbortHandler)
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *IdP) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/auth",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *IdP) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := p.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": KeyID,
		"n": b64.EncodeToString(pub.N.Bytes()),
		"e": b64.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// Authorize plays the browser's visit to the provider: it validates the
// authorization URL the server redirected to like Google would, remembers the
// grant for user and returns the callback URL the provider would redirect back
// to (redirect_uri?code=...&state=...).
func (p *IdP) Authorize(authURL string, user User) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	switch {
	case !strings.HasPrefix(authURL, p.URL+"/auth?"):
		return "", fmt.Errorf("not an authorization URL of this provider: %s", authURL)
	case q.Get("client_id") != p.ClientID:
		return "", fmt.Errorf("unknown client_id %q", q.Get("client_id"))
	case q.Get("response_type") != "code":
		return "", fmt.Errorf("response_type = %q", q.Get("response_type"))
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		return "", fmt.Errorf("PKCE S256 is required")
	case q.Get("state") == "" || q.Get("redirect_uri") == "":
		return "", fmt.Errorf("state and redirect_uri are required")
	case !hasWord(q.Get("scope"), "openid"):
		return "", fmt.Errorf("scope %q lacks openid", q.Get("scope"))
	}
	code := randomString()
	p.mu.Lock()
	p.lastAuth = q
	p.grants[code] = &grant{
		clientID: q.Get("client_id"), redirectURI: q.Get("redirect_uri"),
		challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), user: user,
	}
	p.mu.Unlock()
	return callback(q.Get("redirect_uri"), url.Values{"code": {code}, "state": {q.Get("state")}}), nil
}

// Deny plays the user refusing the consent screen: the callback URL carries
// error=access_denied and the state.
func (p *IdP) Deny(authURL string) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	return callback(q.Get("redirect_uri"), url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}), nil
}

func callback(redirectURI string, v url.Values) string {
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	return redirectURI + sep + v.Encode()
}

func hasWord(s, word string) bool {
	for _, w := range strings.Fields(s) {
		if w == word {
			return true
		}
	}
	return false
}

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return b64.EncodeToString(b)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": "fake provider: " + code})
}

func (p *IdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	} else if id2, err := url.QueryUnescape(id); err == nil {
		id = id2
		secret, _ = url.QueryUnescape(secret)
	}
	form := url.Values{}
	for k, v := range r.PostForm {
		if k != "client_secret" {
			form[k] = v
		}
	}
	p.mu.Lock()
	p.lastToken = form
	hook, now := p.onToken, p.now
	p.mu.Unlock()

	if id != p.ClientID || secret != p.ClientSecret {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	p.mu.Lock()
	g := p.grants[r.PostForm.Get("code")]
	var bad string
	switch {
	case g == nil || g.used:
		bad = "invalid_grant"
	case g.clientID != id || g.redirectURI != r.PostForm.Get("redirect_uri"):
		bad = "invalid_grant"
	case pkceChallenge(r.PostForm.Get("code_verifier")) != g.challenge:
		bad = "invalid_grant"
	}
	if g != nil && bad == "" {
		g.used = true
	}
	p.mu.Unlock()
	if bad != "" {
		oauthError(w, http.StatusBadRequest, bad)
		return
	}

	issued := now()
	spec := &TokenSpec{
		Header: map[string]any{"alg": "RS256", "typ": "JWT", "kid": KeyID},
		Claims: map[string]any{
			"iss": p.URL, "sub": g.user.Sub, "aud": p.ClientID, "azp": p.ClientID,
			"iat": issued.Unix(), "exp": issued.Add(time.Hour).Unix(), "nonce": g.nonce, "name": g.user.Name,
		},
	}
	if hook != nil {
		hook(spec)
	}
	if spec.Delay > 0 {
		select {
		case <-time.After(spec.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if spec.Status != 0 {
		oauthError(w, spec.Status, "server_error")
		return
	}
	resp := map[string]any{
		"access_token": "fake-access-token-" + randomString(), "token_type": "Bearer", "expires_in": 3600,
	}
	if !spec.OmitIDToken {
		resp["id_token"] = p.sign(spec)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64.EncodeToString(sum[:])
}

// Sign returns a JWT signed per spec, for tests that need a token without going
// through the token endpoint.
func (p *IdP) Sign(spec *TokenSpec) string { return p.sign(spec) }

func (p *IdP) sign(spec *TokenSpec) string {
	h, _ := json.Marshal(spec.Header)
	c, _ := json.Marshal(spec.Claims)
	signingInput := b64.EncodeToString(h) + "." + b64.EncodeToString(c)

	var sig []byte
	switch {
	case spec.Unsigned:
	case spec.Header["alg"] == "HS256":
		m := hmac.New(sha256.New, spec.HMACKey)
		m.Write([]byte(signingInput))
		sig = m.Sum(nil)
	default:
		key := spec.SignWith
		if key == nil {
			key = p.key
		}
		// RS256 unless the header says RS512: a correct signature with an
		// algorithm the server did not ask for.
		hash, sum := crypto.SHA256, sha256.Sum256([]byte(signingInput))
		digest := sum[:]
		if spec.Header["alg"] == "RS512" {
			sum512 := sha512.Sum512([]byte(signingInput))
			hash, digest = crypto.SHA512, sum512[:]
		}
		var err error
		if sig, err = rsa.SignPKCS1v15(rand.Reader, key, hash, digest); err != nil {
			panic(err)
		}
	}
	if spec.CorruptSignature && len(sig) > 0 {
		sig[len(sig)/2] ^= 0x01
	}
	return signingInput + "." + b64.EncodeToString(sig)
}
