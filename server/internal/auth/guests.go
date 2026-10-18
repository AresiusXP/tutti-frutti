// Package auth identifies guests: a signed, HttpOnly cookie carries a random
// guest ID that doubles as the player ID. It also holds the Origin check
// shared by the REST API and the WebSocket endpoint, and the account side
// (docs/DESIGN.md section 9b): Sessions (the signed tf_session cookie of a
// logged-in user), Google (the OpenID Connect authorization-code flow with
// PKCE, with a lazy discovery and a verified ID token) and SafeReturnTo (the
// allow-list of where a login returns to). The guest, session and OAuth
// cookies are signed with keys derived from the one SESSION_SECRET with
// different labels, so none of them can be replayed as another.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

const (
	idBytes = 16
	// cookieMaxAge is one year, in seconds.
	cookieMaxAge = 365 * 24 * 60 * 60

	// A valid cookie is exactly <idLen chars>.<macLen chars>; any other
	// length (including oversized values) is rejected without hashing.
	idLen  = 22 // base64url(16 bytes), unpadded
	macLen = 43 // base64url(32 bytes), unpadded

	// MinSecretBytes is the length below which NewGuests warns that the
	// secret is weak (config.Load refuses such a SESSION_SECRET outright).
	MinSecretBytes = 32
)

var enc = base64.RawURLEncoding

// Guests issues and verifies guest cookies. It is safe for concurrent use.
type Guests struct {
	secret []byte
	secure bool
}

// NewGuests builds a Guests signing with secret. When secret is empty a
// random 32-byte secret is generated and a warning is logged: sessions then
// do not survive a restart. A non-empty secret shorter than MinSecretBytes is
// still used, with a warning (the value is never logged). secure sets the
// cookie's Secure attribute (true when PUBLIC_URL is https). A nil log uses
// slog.Default().
func NewGuests(secret []byte, secure bool, log *slog.Logger) *Guests {
	if log == nil {
		log = slog.Default()
	}
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
		log.Warn("SESSION_SECRET is empty: using a random ephemeral secret, guest sessions will not survive a restart")
	} else {
		if len(secret) < MinSecretBytes {
			log.Warn("SESSION_SECRET is shorter than the recommended minimum: a guessable secret lets anyone holding one cookie forge others",
				"length", len(secret), "min", MinSecretBytes)
		}
		secret = append([]byte(nil), secret...)
	}
	return &Guests{secret: secret, secure: secure}
}

func (g *Guests) mac(id string) []byte {
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(id))
	return m.Sum(nil)
}

// Identify returns the guest ID carried by a valid cookie of r. A missing,
// garbled, oversized or forged cookie is not ok.
func (g *Guests) Identify(r *http.Request) (string, bool) {
	for _, c := range r.CookiesNamed(protocol.GuestCookie) {
		if id, ok := g.verify(c.Value); ok {
			return id, true
		}
	}
	return "", false
}

func (g *Guests) verify(value string) (string, bool) {
	if len(value) != idLen+1+macLen {
		return "", false
	}
	id, mac, ok := strings.Cut(value, ".")
	if !ok || len(id) != idLen || len(mac) != macLen {
		return "", false
	}
	// Strict: the last base64 character carries 2 unused bits; a MAC that
	// differs only there must not be accepted as another spelling of the same
	// cookie.
	got, err := enc.Strict().DecodeString(mac)
	if err != nil || !hmac.Equal(got, g.mac(id)) {
		return "", false
	}
	// The id is the exact string that was signed; make sure it is the
	// canonical base64url of 16 bytes so IDs never alias.
	raw, err := enc.DecodeString(id)
	if err != nil || len(raw) != idBytes || enc.EncodeToString(raw) != id {
		return "", false
	}
	return id, true
}

// Issue mints a new guest ID, sets its cookie on w and returns the ID.
func (g *Guests) Issue(w http.ResponseWriter) string {
	raw := make([]byte, idBytes)
	_, _ = rand.Read(raw)
	id := enc.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     protocol.GuestCookie,
		Value:    id + "." + enc.EncodeToString(g.mac(id)),
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   g.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return id
}

// Ensure returns the guest ID of r, minting (and setting the cookie for) a new
// one when r has no valid cookie.
func (g *Guests) Ensure(w http.ResponseWriter, r *http.Request) string {
	if id, ok := g.Identify(r); ok {
		return id
	}
	return g.Issue(w)
}
