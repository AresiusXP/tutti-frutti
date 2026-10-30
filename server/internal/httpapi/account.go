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
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
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

// authenticate returns the user id of the session cookie, or answers 401. A
// valid cookie whose account was deleted (a copy that outlived it) is a lost
// session like any other: 401, and the browser is told to drop it. A store that
// cannot tell is a 500 and the cookie stays (the account may well exist).
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, status := s.sessions.Lookup(r)
	switch status {
	case auth.SessionActive:
		return id, true
	case auth.SessionUnknown:
		s.log.Error("account request failed", "what", "look up the session's account")
		writeError(w, http.StatusInternalServerError, "internal")
	default:
		s.unauthorized(w, r)
	}
	return "", false
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
	// A saved nickname is shown to other players: the guest on this device has
	// to have accepted the terms (unknown fails closed).
	if !s.requireTermsOfRequest(w, r) {
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
	s.forgetAccount(r.Context(), id)
	s.sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// forgetAccount is what happens after the store deleted an account, before the
// response (see docs/DESIGN.md, "Deleting an account while a game is on"):
//
//  1. the sessions forget it, so a copy of its cookie stops identifying anybody
//     at once (REST, the WebSocket upgrade, reports) instead of when a cache
//     expires;
//  2. every room drops its links to it and the sockets that were opened as that
//     user lose the account (no AI judge, no statistics), keeping the guest seat.
//
// The order matters: the store first (so nothing can resolve the account
// again), then the sessions (so no new connection can pick it up), then the
// rooms. A room failing to answer in time does not undo the deletion, and it
// does not leave a window either: the rooms were handed the order before this
// returns, every frame they receive afterwards queues behind it, and a
// connection made after the sessions forgot is a guest.
func (s *Server) forgetAccount(parent context.Context, id string) {
	s.sessions.Forget(id)
	f, ok := s.rooms.(room.AccountForgetter)
	if !ok {
		return
	}
	// The account is gone already: finish even if the client hung up meanwhile.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), accountTimeout)
	defer cancel()
	if err := f.ForgetAccount(ctx, id); err != nil {
		s.log.Error("the rooms could not confirm they forgot a deleted account; they will drop it when they get to it", "err", err)
	}
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
