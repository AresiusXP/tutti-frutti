package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Cookie names of the account flow.
const (
	// SessionCookie carries the signed-in user: <userId>.<expiryUnix>.<mac>.
	SessionCookie = "tf_session"
	// OAuthCookie carries the transaction of a Google login in flight.
	OAuthCookie = "tf_oauth"
)

const (
	// SessionMaxAge is the lifetime of a session cookie.
	SessionMaxAge = 30 * 24 * time.Hour

	// Labels that separate the keys derived from SESSION_SECRET: the guest
	// cookie is signed with the secret itself, the session and the OAuth
	// transaction cookies with a key derived with their own label, so a MAC
	// minted for one purpose never verifies for another.
	sessionKeyLabel = "tf-session-v1"
	oauthKeyLabel   = "tf-oauth-v1"

	// A session value is exactly <36-char uuid>.<1..11 digits>.<43-char mac>;
	// anything longer is refused before any hashing.
	userIDLen       = 36
	maxExpiryDigits = 11
	maxSessionLen   = userIDLen + 1 + maxExpiryDigits + 1 + macLen
	// maxExpirySkew is how far past SessionMaxAge an expiry may lie: the
	// signed expiry is ours, so one that far ahead is not.
	maxExpirySkew = time.Minute
)

var errBadUserID = errors.New("auth: malformed user id")

// NewSecret returns the secret every signed cookie derives its key from: a copy
// of secret, or, when it is empty, a random ephemeral one (cookies then do not
// survive a restart) with a warning. A non-empty secret shorter than
// MinSecretBytes is used with a warning too (the value is never logged). A nil
// log uses slog.Default().
func NewSecret(secret []byte, log *slog.Logger) []byte {
	if log == nil {
		log = slog.Default()
	}
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
		log.Warn("SESSION_SECRET is empty: using a random ephemeral secret, sessions will not survive a restart")
		return secret
	}
	if len(secret) < MinSecretBytes {
		log.Warn("SESSION_SECRET is shorter than the recommended minimum: a guessable secret lets anyone holding one cookie forge others",
			"length", len(secret), "min", MinSecretBytes)
	}
	return append([]byte(nil), secret...)
}

func deriveKey(secret []byte, label string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(label))
	return m.Sum(nil)
}

func signMAC(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// verifyMAC reports whether mac (base64url, unpadded) is the MAC of msg under
// key, in constant time. The encoding is strict: a MAC that differs from the
// canonical one only in the unused bits of its last character is refused.
func verifyMAC(key []byte, msg, mac string) bool {
	if len(mac) != macLen {
		return false
	}
	got, err := enc.Strict().DecodeString(mac)
	return err == nil && hmac.Equal(got, signMAC(key, msg))
}

// Sessions issues and verifies the account cookie. It is stateless: a session
// is the user id and an expiry, signed. It is safe for concurrent use.
type Sessions struct {
	key    []byte
	secure bool
	now    func() time.Time
}

// SessionOption customises NewSessions.
type SessionOption func(*Sessions)

// WithSessionClock makes the sessions use now instead of time.Now (tests).
func WithSessionClock(now func() time.Time) SessionOption {
	return func(s *Sessions) { s.now = now }
}

// NewSessions builds Sessions signing with a key derived from secret (see
// NewSecret; an empty secret is a programming error and panics). secure sets
// the cookie's Secure attribute (true when PUBLIC_URL is https).
func NewSessions(secret []byte, secure bool, opts ...SessionOption) *Sessions {
	if len(secret) == 0 {
		panic("auth: NewSessions needs a secret (see NewSecret)")
	}
	s := &Sessions{key: deriveKey(secret, sessionKeyLabel), secure: secure, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Identify returns the user id carried by a valid, unexpired session cookie of
// r. A missing, garbled, oversized, forged or expired cookie is not ok.
func (s *Sessions) Identify(r *http.Request) (string, bool) {
	for _, c := range r.CookiesNamed(SessionCookie) {
		if id, ok := s.verify(c.Value); ok {
			return id, true
		}
	}
	return "", false
}

func (s *Sessions) verify(value string) (string, bool) {
	if len(value) > maxSessionLen {
		return "", false
	}
	id, rest, ok := strings.Cut(value, ".")
	if !ok || len(id) != userIDLen || !store.ValidID(id) {
		return "", false
	}
	exp, mac, ok := strings.Cut(rest, ".")
	if !ok || len(mac) != macLen || !canonicalUnix(exp) {
		return "", false
	}
	if !verifyMAC(s.key, id+"."+exp, mac) {
		return "", false
	}
	// The MAC is good: the expiry is ours and parses.
	secs, _ := strconv.ParseInt(exp, 10, 64)
	now := s.now()
	until := time.Unix(secs, 0).Sub(now)
	if until <= 0 || until > SessionMaxAge+maxExpirySkew {
		return "", false
	}
	return id, true
}

// canonicalUnix reports whether s is the decimal form of a positive Unix
// time: 1 to maxExpiryDigits digits, no sign, no leading zero.
func canonicalUnix(s string) bool {
	if len(s) == 0 || len(s) > maxExpiryDigits || s[0] == '0' {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Issue sets the session cookie of userID on w. It refuses a malformed id.
func (s *Sessions) Issue(w http.ResponseWriter, userID string) error {
	if !store.ValidID(userID) {
		return errBadUserID
	}
	exp := strconv.FormatInt(s.now().Add(SessionMaxAge).Unix(), 10)
	msg := userID + "." + exp
	http.SetCookie(w, s.cookie(msg+"."+enc.EncodeToString(signMAC(s.key, msg)), int(SessionMaxAge/time.Second)))
	return nil
}

// Clear expires the session cookie on the client (also when there is none).
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, s.cookie("", -1))
}

func (s *Sessions) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}
