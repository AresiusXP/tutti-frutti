package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	// GoogleIssuer is Google's OpenID Connect issuer: the discovery document
	// lives under it and every ID token must name it in "iss".
	GoogleIssuer = "https://accounts.google.com"

	// GoogleCallbackPath is the path of the redirect URI, under PUBLIC_URL.
	GoogleCallbackPath = "/api/auth/google/callback"
	// googleCookiePath is the Path of the transaction cookie: it is only sent
	// to the login and callback endpoints.
	googleCookiePath = "/api/auth/google"

	// OAuthTTL is how long a login may take between the redirect to Google and
	// the callback.
	OAuthTTL = 10 * time.Minute
	// maxOAuthTTLSkew: see maxExpirySkew.
	maxOAuthTTLSkew = time.Minute

	// A transaction cookie is <base64url(json)>.<43-char mac>.
	maxOAuthCookieLen = 1024
	// secretLen is the length of state, nonce and PKCE verifier (43 base64url
	// characters: 32 random bytes, or the RFC 7636 verifier of x/oauth2).
	secretLen = 43

	// maxCodeLen bounds the authorization code taken from the query string.
	maxCodeLen = 2048
	// maxSubjectLen is Google's documented maximum for "sub".
	maxSubjectLen = 255
	// MaxDisplayNameRunes cuts the Google display name (the default nickname).
	MaxDisplayNameRunes = 16

	// googleTimeout bounds every request to Google (discovery, JWKS, token).
	googleTimeout = 10 * time.Second
	// maxGoogleBody bounds every response body read from Google.
	maxGoogleBody = 1 << 20
)

// Reasons of a failed login: the only thing the logs say about it.
const (
	reasonDiscovery = "discovery"
	reasonState     = "state"
	reasonParams    = "params"
	reasonExchange  = "exchange"
	reasonIDToken   = "id_token"
	reasonNonce     = "nonce"
	reasonClaims    = "claims"
	reasonProvider  = "provider_error"
)

// ErrLoginCancelled: the user refused (or abandoned) the consent screen
// (error=access_denied).
var ErrLoginCancelled = errors.New("auth: login cancelled")

// ErrLoginFailed wraps every other way a login can fail; its message names the
// stage, never a secret.
var ErrLoginFailed = errors.New("auth: login failed")

func loginFailed(reason string) error { return fmt.Errorf("%w: %s", ErrLoginFailed, reason) }

// Identity is what a successful Google login proves.
type Identity struct {
	// Subject is Google's stable, opaque id of the account ("sub").
	Subject string
	// Name is the display name, cleaned and cut to MaxDisplayNameRunes; it can
	// be empty.
	Name string
}

// GoogleConfig configures Google. ClientID, ClientSecret, RedirectURL and
// Secret are required. The remaining fields exist for tests (a fake identity
// provider) and are not wired to any environment variable.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is PUBLIC_URL + GoogleCallbackPath.
	RedirectURL string
	// Secret is SESSION_SECRET (see NewSecret): the transaction cookie is
	// signed with a key derived from it.
	Secret []byte
	// Secure sets the Secure attribute of the transaction cookie (https).
	Secure bool
	Logger *slog.Logger

	// Issuer defaults to GoogleIssuer.
	Issuer string
	// Transport defaults to a clone of http.DefaultTransport. The client built
	// on it never follows redirects, has a 10 s timeout and refuses bodies
	// over 1 MiB.
	Transport http.RoundTripper
	// Timeout defaults to 10 s.
	Timeout time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// Google runs the OpenID Connect authorization-code flow with PKCE against
// Google. It is safe for concurrent use.
//
// Discovery is lazy: nothing touches the network until the first login, and a
// failed discovery is retried by the next one, so Google being unreachable
// never delays or fails the start of the server.
type Google struct {
	cfg    GoogleConfig
	log    *slog.Logger
	key    []byte
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewGoogle builds the flow. It panics on a missing required field (a
// programming error: cmd/server only builds it when Google is configured).
func NewGoogle(cfg GoogleConfig) *Google {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" || len(cfg.Secret) == 0 {
		panic("auth: GoogleConfig needs ClientID, ClientSecret, RedirectURL and Secret")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = GoogleIssuer
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = googleTimeout
	}
	base := cfg.Transport
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	g := &Google{
		cfg: cfg,
		log: cfg.Logger,
		key: deriveKey(cfg.Secret, oauthKeyLabel),
		now: cfg.Now,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: boundedTransport{base: base},
			// The token request carries the client secret: never replay it
			// somewhere else because a redirect said so.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if g.log == nil {
		g.log = slog.Default()
	}
	if g.now == nil {
		g.now = time.Now
	}
	return g
}

// boundedTransport refuses response bodies larger than maxGoogleBody (go-oidc
// reads the discovery document and the key set without a limit).
type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedBody{rc: resp.Body, left: maxGoogleBody}
	return resp, nil
}

var errBodyTooLarge = errors.New("auth: response body too large")

type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, errBodyTooLarge
	}
	// One byte more than allowed tells "exactly at the limit" from "over".
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n, errBodyTooLarge
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// ready performs (or reuses) the provider discovery.
func (g *Google) ready(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.oauth != nil {
		return g.oauth, g.verifier, nil
	}
	ctx = oidc.ClientContext(ctx, g.client)
	provider, err := oidc.NewProvider(ctx, g.cfg.Issuer)
	if err != nil {
		return nil, nil, err
	}
	g.oauth = &oauth2.Config{
		ClientID:     g.cfg.ClientID,
		ClientSecret: g.cfg.ClientSecret,
		RedirectURL:  g.cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile"},
	}
	g.verifier = provider.Verifier(&oidc.Config{
		ClientID:             g.cfg.ClientID,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  g.now,
	})
	return g.oauth, g.verifier, nil
}

// oauthTx is the transaction stored in the tf_oauth cookie.
type oauthTx struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
	ReturnTo string `json:"r"`
	Expires  int64  `json:"e"`
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return enc.EncodeToString(b)
}

// Start begins a login: it stores a new transaction (state, PKCE verifier,
// nonce and the sanitised returnTo) in the signed tf_oauth cookie on w and
// returns the authorization URL to send the browser to. It fails, setting
// nothing, when Google's discovery document cannot be fetched.
func (g *Google) Start(ctx context.Context, w http.ResponseWriter, returnTo string) (string, error) {
	oc, _, err := g.ready(ctx)
	if err != nil {
		g.log.Warn("google login: discovery failed", "err", errDetail(err))
		return "", loginFailed(reasonDiscovery)
	}
	tx := oauthTx{
		State:    randomToken(),
		Verifier: oauth2.GenerateVerifier(),
		Nonce:    randomToken(),
		ReturnTo: SafeReturnTo(returnTo),
		Expires:  g.now().Add(OAuthTTL).Unix(),
	}
	g.setCookie(w, g.signTx(tx), int(OAuthTTL/time.Second))

	return oc.AuthCodeURL(tx.State,
		oauth2.S256ChallengeOption(tx.Verifier),
		oauth2.SetAuthURLParam("nonce", tx.Nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	), nil
}

// signTx encodes tx as <base64url(json)>.<mac>.
func (g *Google) signTx(tx oauthTx) string {
	payload, _ := json.Marshal(tx)
	body := enc.EncodeToString(payload)
	return body + "." + enc.EncodeToString(signMAC(g.key, body))
}

func (g *Google) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthCookie,
		Value:    value,
		Path:     googleCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   g.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// readTx returns the first valid, unexpired transaction cookie of r.
func (g *Google) readTx(r *http.Request) (oauthTx, bool) {
	for _, c := range r.CookiesNamed(OAuthCookie) {
		if tx, ok := g.parseTx(c.Value); ok {
			return tx, true
		}
	}
	return oauthTx{}, false
}

func (g *Google) parseTx(value string) (oauthTx, bool) {
	if len(value) > maxOAuthCookieLen {
		return oauthTx{}, false
	}
	body, mac, ok := strings.Cut(value, ".")
	if !ok || !verifyMAC(g.key, body, mac) {
		return oauthTx{}, false
	}
	payload, err := enc.DecodeString(body)
	if err != nil {
		return oauthTx{}, false
	}
	var tx oauthTx
	if err := json.Unmarshal(payload, &tx); err != nil {
		return oauthTx{}, false
	}
	until := time.Unix(tx.Expires, 0).Sub(g.now())
	switch {
	case until <= 0 || until > OAuthTTL+maxOAuthTTLSkew,
		len(tx.State) != secretLen, len(tx.Verifier) != secretLen, len(tx.Nonce) != secretLen,
		SafeReturnTo(tx.ReturnTo) != tx.ReturnTo:
		return oauthTx{}, false
	}
	return tx, true
}

// ClearTransaction expires the tf_oauth cookie on w without looking at the
// request: a callback that is turned away before Finish (rate limit) must not
// leave the transaction valid for the rest of its TTL.
func (g *Google) ClearTransaction(w http.ResponseWriter) { g.setCookie(w, "", -1) }

// Finish completes a login from the callback request r: it checks the
// transaction cookie against the "state" parameter, exchanges the code (with
// the PKCE verifier), verifies the ID token (signature against Google's keys,
// issuer, audience, expiry, nonce) and returns who logged in and where to send
// the browser (always "/" or "/<ROOMCODE>"). The transaction cookie is expired
// on w whatever the outcome, so a callback can only be used once.
//
// The error is ErrLoginCancelled when the user refused, otherwise it wraps
// ErrLoginFailed.
func (g *Google) Finish(ctx context.Context, w http.ResponseWriter, r *http.Request) (Identity, string, error) {
	g.setCookie(w, "", -1)

	tx, ok := g.readTx(r)
	q := r.URL.Query()
	state := q.Get("state")
	if !ok || len(state) != secretLen || subtle.ConstantTimeCompare([]byte(state), []byte(tx.State)) != 1 {
		return Identity{}, "", loginFailed(reasonState)
	}
	if errParam := q.Get("error"); errParam != "" {
		if errParam == "access_denied" {
			return Identity{}, "", ErrLoginCancelled
		}
		g.log.Warn("google login: the provider reported an error", "error", truncate(errParam, 64))
		return Identity{}, "", loginFailed(reasonProvider)
	}
	code := q.Get("code")
	if code == "" || len(code) > maxCodeLen {
		return Identity{}, "", loginFailed(reasonParams)
	}

	oc, verifier, err := g.ready(ctx)
	if err != nil {
		g.log.Warn("google login: discovery failed", "err", errDetail(err))
		return Identity{}, "", loginFailed(reasonDiscovery)
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	token, err := oc.Exchange(ctx, code, oauth2.VerifierOption(tx.Verifier))
	if err != nil {
		g.log.Warn("google login: code exchange failed", "err", errDetail(err))
		return Identity{}, "", loginFailed(reasonExchange)
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		g.log.Warn("google login: the token response has no id_token")
		return Identity{}, "", loginFailed(reasonIDToken)
	}
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		g.log.Warn("google login: id token rejected", "err", errDetail(err))
		return Identity{}, "", loginFailed(reasonIDToken)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(tx.Nonce)) != 1 {
		g.log.Warn("google login: id token nonce mismatch")
		return Identity{}, "", loginFailed(reasonNonce)
	}

	var claims struct {
		Name            string `json:"name"`
		AuthorizedParty string `json:"azp"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, "", loginFailed(reasonClaims)
	}
	// OIDC core 3.1.3.7: azp, when present, must be us; with several audiences it
	// must be present. One audience and no azp (what Google issues to a web
	// client) needs nothing more than aud.
	if (claims.AuthorizedParty != "" || len(idToken.Audience) > 1) && claims.AuthorizedParty != g.cfg.ClientID {
		g.log.Warn("google login: id token has a foreign or missing azp")
		return Identity{}, "", loginFailed(reasonClaims)
	}
	sub := idToken.Subject
	if sub == "" || len(sub) > maxSubjectLen || strings.ContainsFunc(sub, unicode.IsControl) {
		g.log.Warn("google login: id token has an unusable subject")
		return Identity{}, "", loginFailed(reasonClaims)
	}
	return Identity{Subject: sub, Name: CleanDisplayName(claims.Name)}, SafeReturnTo(tx.ReturnTo), nil
}

// CleanDisplayName makes a Google display name fit for a nickname: invalid
// UTF-8, control and invisible format characters are dropped, whitespace is
// collapsed and trimmed, and the result is cut to MaxDisplayNameRunes.
func CleanDisplayName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > MaxDisplayNameRunes {
		name = strings.TrimSpace(string(runes[:MaxDisplayNameRunes]))
	}
	return name
}

// errDetail describes err for the logs without echoing anything a provider
// could have put in a response body.
func errDetail(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		return fmt.Sprintf("token endpoint answered %d %s", re.Response.StatusCode, truncate(re.ErrorCode, 64))
	}
	return truncate(err.Error(), 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
