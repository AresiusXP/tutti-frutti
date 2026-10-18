package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Logins that end in a redirect to the home screen carry one of these in the
// query string (web: Home shows a toast and removes the parameter).
const (
	loginFailedLocation    = "/?login=failed"
	loginCancelledLocation = "/?login=cancelled"
)

// Values of the "result" label of tuttifrutti_logins_total.
const (
	loginOK        = "ok"
	loginFailed    = "failed"
	loginCancelled = "cancelled"
)

// accountTimeout bounds the store calls of the account endpoints.
const accountTimeout = 10 * time.Second

// maxAvatarBytes is the limit of an avatar emoji or colour (game.Join's).
const maxAvatarBytes = 32

func newLoginMetric() *prometheus.CounterVec {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tuttifrutti_logins_total",
		Help: "Google login attempts that reached the callback, by result (ok, failed or cancelled).",
	}, []string{"result"})
	// Constant cardinality: every series exists from the start.
	for _, r := range []string{loginOK, loginFailed, loginCancelled} {
		v.WithLabelValues(r)
	}
	return v
}

func (s *Server) registerAccounts() {
	if s.sessions == nil || s.accounts == nil {
		return
	}
	s.mux.HandleFunc("GET /api/me", s.handleMe)
	s.mux.HandleFunc("PUT /api/me/profile", s.handleSaveProfile)
	s.mux.HandleFunc("GET /api/me/stats", s.handleStats)
	s.mux.HandleFunc("DELETE /api/me", s.handleDeleteMe)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/google/login", s.handleGoogleLogin)
	s.mux.HandleFunc("GET /api/auth/google/callback", s.handleGoogleCallback)
}

// unauthorized answers 401 and expires a session cookie the browser still
// sends, so it stops doing so.
func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(auth.SessionCookie); err == nil {
		s.sessions.Clear(w)
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

// authenticate returns the user id of the session cookie, or answers 401.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := s.sessions.Identify(r)
	if !ok {
		s.unauthorized(w, r)
	}
	return id, ok
}

// accountError answers a failed store call: a user that no longer exists is
// a lost session (401), anything else a server error.
func (s *Server) accountError(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, store.ErrUserNotFound) {
		s.unauthorized(w, r)
		return
	}
	s.log.Error("account request failed", "what", what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal")
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), accountTimeout)
	defer cancel()
	u, err := s.accounts.GetUser(ctx, id)
	if err != nil {
		s.accountError(w, r, "get user", err)
		return
	}
	writeJSON(w, http.StatusOK, api.UserInfo{ID: u.ID, Name: u.Name, Profile: u.Profile})
}

func (s *Server) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	var req api.Profile
	if err := decodeBody(w, r, &req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	prof, code := cleanProfile(req)
	if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), accountTimeout)
	defer cancel()
	if err := s.accounts.SaveProfile(ctx, id, prof); err != nil {
		s.accountError(w, r, "save profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cleanProfile validates a profile like a game join does (game.CleanName, the
// avatar field limit) and returns it cleaned, or the error code. Control
// characters are refused on top of that: they have no business in a nickname
// or an emoji.
func cleanProfile(p api.Profile) (api.Profile, string) {
	// game.CleanName strips control characters instead of refusing them (a
	// player's join should not fail over a stray byte), so look at the raw
	// value: a profile that carries one is a broken client, not a nickname.
	if strings.ContainsFunc(p.Nickname, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) {
		return api.Profile{}, "invalid_name"
	}
	name, err := game.CleanName(p.Nickname)
	if err != nil {
		return api.Profile{}, "invalid_name"
	}
	emoji, color := strings.TrimSpace(p.Avatar.Emoji), strings.TrimSpace(p.Avatar.Color)
	if !validAvatarField(emoji) || !validAvatarField(color) {
		return api.Profile{}, "invalid_avatar"
	}
	return api.Profile{Nickname: name, Avatar: game.Avatar{Emoji: emoji, Color: color}}, ""
}

func validAvatarField(s string) bool {
	return s != "" && len(s) <= maxAvatarBytes && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), accountTimeout)
	defer cancel()
	stats, err := s.accounts.UserStats(ctx, id)
	if err != nil {
		s.accountError(w, r, "user stats", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), accountTimeout)
	defer cancel()
	if err := s.accounts.DeleteUser(ctx, id); err != nil {
		s.accountError(w, r, "delete user", err)
		return
	}
	s.sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	s.sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// redirect answers 302 to a location the server built itself; there is no
// body, so nothing of the request can be echoed.
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

// allowAuth charges one request to the shared bucket of the login endpoints.
func (s *Server) allowAuth(r *http.Request) (bool, time.Duration) {
	return s.authLimit.Allow(s.limitKey(r))
}

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		writeError(w, http.StatusNotFound, "auth_disabled")
		return
	}
	if ok, retry := s.allowAuth(r); !ok {
		ratelimit.Reject(w, retry)
		return
	}
	authURL, err := s.google.Start(r.Context(), w, r.URL.Query().Get("returnTo"))
	if err != nil {
		// The browser is mid-navigation: bring it home with the toast rather
		// than leave it on a JSON error.
		s.logins.WithLabelValues(loginFailed).Inc()
		redirect(w, loginFailedLocation)
		return
	}
	redirect(w, authURL)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		writeError(w, http.StatusNotFound, "auth_disabled")
		return
	}
	// A callback is always answered with a redirect, rate limited or not.
	if ok, _ := s.allowAuth(r); !ok {
		s.google.ClearTransaction(w)
		s.logins.WithLabelValues(loginFailed).Inc()
		redirect(w, loginFailedLocation)
		return
	}
	ident, returnTo, err := s.google.Finish(r.Context(), w, r)
	switch {
	case errors.Is(err, auth.ErrLoginCancelled):
		s.logins.WithLabelValues(loginCancelled).Inc()
		redirect(w, loginCancelledLocation)
		return
	case err != nil:
		s.logins.WithLabelValues(loginFailed).Inc()
		redirect(w, loginFailedLocation)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), accountTimeout)
	defer cancel()
	user, err := s.accounts.UpsertGoogleUser(ctx, ident.Subject, ident.Name)
	if err == nil {
		err = s.sessions.Issue(w, user.ID)
	}
	if err != nil {
		s.log.Error("login: creating the session failed", "err", err)
		s.logins.WithLabelValues(loginFailed).Inc()
		redirect(w, loginFailedLocation)
		return
	}
	s.logins.WithLabelValues(loginOK).Inc()
	redirect(w, returnTo)
}
