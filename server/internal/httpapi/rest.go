package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

// maxBodyBytes bounds JSON request bodies.
const maxBodyBytes = 4 << 10

// roomCodeAlphabet is the alphabet of room codes (no vowels, no look-alikes).
const roomCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ23456789"

const roomCodeLen = 4

// createRoomRequest is the body of POST /api/rooms.
type createRoomRequest = api.CreateRoomRequest

// Listing is optional for transport doubles, but implemented by the room hub.
type publicRoomLister interface {
	PublicRooms(game.Lang) []api.PublicRoom
}

func (s *Server) registerREST() {
	s.mux.HandleFunc("GET /api/categories", s.handleCategories)
	if s.guests != nil {
		s.mux.HandleFunc("GET /api/session", s.handleSession)
	}
	if s.rooms == nil {
		return
	}
	if _, ok := s.rooms.(publicRoomLister); ok {
		s.mux.Handle("GET /api/rooms",
			ratelimit.Middleware(s.browseLimit, s.limitKey)(http.HandlerFunc(s.handlePublicRooms)))
	}
	s.mux.Handle("GET /api/rooms/{code}",
		ratelimit.Middleware(s.lookupLimit, s.limitKey)(http.HandlerFunc(s.handleRoomInfo)))
	if s.guests != nil {
		s.mux.Handle("POST /api/rooms",
			ratelimit.Middleware(s.createLimit, s.limitKey)(http.HandlerFunc(s.handleCreateRoom)))
	}
}

// limitKey is the rate-limit key of a request: its client IP.
func (s *Server) limitKey(r *http.Request) string {
	return ratelimit.KeyForIP(ratelimit.ClientIP(r, s.trustXFF))
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	guestID, fresh := s.guestOf(w, r)
	info := api.SessionInfo{Guest: true, GoogleEnabled: s.google != nil}
	if s.sessions != nil {
		// loggedIn is a valid cookie whose account still exists; the cookie of a
		// deleted account is expired here too.
		var status auth.SessionStatus
		_, status = s.sessions.Lookup(r)
		info.LoggedIn = status == auth.SessionActive
		if status == auth.SessionGone {
			s.sessions.Clear(w)
		}
	}
	terms, err := s.termsStatus(r.Context(), guestID, fresh)
	if err != nil {
		// Unknown acceptance is never reported as "not accepted": the client
		// retries (it never gives up on a 503).
		s.log.Warn("the terms gate could not tell; session answered 503", "err", err)
		retryable(w)
		return
	}
	info.Terms = terms
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	lang := game.Lang(r.URL.Query().Get("lang"))
	switch {
	case lang == "":
		lang = game.LangES
	case !lang.Valid():
		writeError(w, http.StatusBadRequest, "invalid_lang")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, catalog.ForLang(string(lang)))
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}

	var req createRoomRequest
	if err := decodeBody(w, r, &req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if req.Lang != "" && !req.Lang.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_lang")
		return
	}
	if req.Visibility != "" && req.Visibility != "private" && req.Visibility != "public" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}

	playerID, fresh := s.guestOf(w, r)
	if !s.requireTerms(w, r, playerID, fresh) {
		return
	}
	userID := ""
	if s.sessions != nil {
		userID, _ = s.sessions.Identify(r)
	}
	code, err := s.rooms.Create(r.Context(), room.CreateRequest{
		PlayerID:   playerID,
		UserID:     userID,
		Name:       req.Name,
		Avatar:     req.Avatar,
		Lang:       req.Lang,
		Visibility: req.Visibility,
	})
	if err != nil {
		s.writeCreateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.CreateRoomResponse{Code: code})
}

func (s *Server) handlePublicRooms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	lang := game.Lang(r.URL.Query().Get("lang"))
	if lang != "" && !lang.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_lang")
		return
	}
	draining := !s.ready.Load()
	if d, ok := s.rooms.(interface{ ShuttingDown() bool }); ok {
		draining = draining || d.ShuttingDown()
	}
	if draining {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "server_busy")
		return
	}
	var rooms []api.PublicRoom
	if l, ok := s.rooms.(callerLister); ok {
		// The caller's own blocks decide what it sees; anonymous callers see all.
		rooms = l.PublicRoomsFor(lang, s.callerOf(r))
	} else {
		rooms = s.rooms.(publicRoomLister).PublicRooms(lang)
	}
	if rooms == nil {
		rooms = []api.PublicRoom{}
	}
	writeJSON(w, http.StatusOK, api.PublicRoomsResponse{Rooms: rooms})
}

func (s *Server) writeCreateError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, room.ErrRestricted) {
		// Generic on purpose, like the refusal of a join: it never says why.
		writeError(w, http.StatusForbidden, api.ErrCodeJoinUnavailable)
		return
	}
	if errors.Is(err, room.ErrTooManyRooms) || errors.Is(err, room.ErrShuttingDown) {
		writeError(w, http.StatusServiceUnavailable, "server_busy")
		return
	}
	if code := game.ErrorCode(err); code != "internal" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	s.log.Error("create room failed", "err", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal")
}

func (s *Server) handleRoomInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	code, ok := normalizeRoomCode(r.PathValue("code"))
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found")
		return
	}
	var (
		info  room.Info
		found bool
	)
	if c, ok := s.rooms.(callerInfo); ok {
		info, found = c.InfoFor(code, s.callerOf(r))
	} else {
		info, found = s.rooms.Info(code)
	}
	if !found {
		// While the server drains (deploy / node reboot) rooms vanish from
		// memory but are restored from their snapshot by the next pod, so
		// "not found" would be a lie that makes clients give up. Tell them to
		// retry instead.
		if d, ok := s.rooms.(interface{ ShuttingDown() bool }); ok && d.ShuttingDown() {
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, "server_busy")
			return
		}
		writeError(w, http.StatusNotFound, "room_not_found")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// normalizeRoomCode trims s, upper-cases its ASCII letters and reports whether
// the result is a well-formed room code (4 characters of the code alphabet).
func normalizeRoomCode(s string) (string, bool) {
	b := []byte(strings.TrimSpace(s))
	if len(b) != roomCodeLen {
		return "", false
	}
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
			b[i] = c
		}
		if strings.IndexByte(roomCodeAlphabet, c) < 0 {
			return "", false
		}
	}
	return string(b), true
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// decodeBody reads one JSON value of at most maxBodyBytes into v, rejecting
// unknown fields and trailing data.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the JSON value")
		}
		return err
	}
	return nil
}
