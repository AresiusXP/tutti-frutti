package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Limits of the block endpoints (docs/DESIGN.md, "Safety"): a block or an unblock
// is a database write plus a sweep of every live room, so each guest gets 30 an
// hour and a global per-IP bucket (like reports, effectively global behind the
// homelab's Traefik) caps what cookie churn can write.
const (
	blocksPerHour = 30
	blockIPRate   = rate.Limit(30.0 / 60)
	blockIPBurst  = 10
	// Reading one's own list is cheap, but it is a database read.
	blockReadRate  = rate.Limit(300.0 / 60)
	blockReadBurst = 60
)

// ReportObserver is told about every report that was stored (the open-report
// gauges of the operator's metrics).
type ReportObserver interface {
	ReportCreated(at time.Time)
}

// policySweeper is what the block endpoints need from the room hub: after the
// policy changed, make every live room apply it and say whom it removed.
// *room.Hub implements it.
type policySweeper interface {
	Sweep(ctx context.Context) (map[string][]string, error)
}

// callerRooms are the optional Rooms methods that know who is asking, so a
// caller never sees a room that holds a human it shares a block with. *room.Hub
// implements them; a Rooms double without them shows everybody everything.
type callerInfo interface {
	InfoFor(code, guestID string) (room.Info, bool)
}

type callerLister interface {
	PublicRoomsFor(lang game.Lang, guestID string) []api.PublicRoom
}

func (s *Server) registerBlocks() {
	if s.moderation == nil || s.guests == nil || s.rooms == nil {
		return
	}
	if _, ok := s.rooms.(policySweeper); !ok {
		return // never register a block that nobody can enforce
	}
	s.mux.Handle("POST /api/blocks",
		ratelimit.Middleware(s.blockIPLimit, s.limitKey)(http.HandlerFunc(s.handleBlock)))
	s.mux.Handle("DELETE /api/blocks/{playerId}",
		ratelimit.Middleware(s.blockIPLimit, s.limitKey)(http.HandlerFunc(s.handleUnblock)))
	s.mux.Handle("GET /api/blocks",
		ratelimit.Middleware(s.blockReads, s.limitKey)(http.HandlerFunc(s.handleListBlocks)))
}

// keyedMutex serialises critical sections per key; the zero value is ready.
// Entries exist only while somebody holds or waits for the key, so the map
// stays as small as the number of requests in flight.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int // the holder plus the goroutines waiting
}

// Lock blocks until the key is free and returns the function that releases it.
func (k *keyedMutex) Lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// cleanBlockName cleans the nickname the blocker saw and cuts it to the limit.
func cleanBlockName(name string) string {
	name = game.CleanText(name)
	if utf8.RuneCountInString(name) > safety.MaxBlockNameRunes {
		name = string([]rune(name)[:safety.MaxBlockNameRunes])
	}
	return name
}

func blockedPlayer(b safety.Block) api.BlockedPlayer {
	return api.BlockedPlayer{PlayerID: b.Blocked, Name: b.BlockedName, BlockedAt: b.CreatedAt.UnixMilli()}
}

// handleBlock is "Block and leave". The order is the enforcement argument
// (room/policy.go): the row is durable, then the policy knows the pair, then
// every live room has applied it, and only then does the blocker hear 200.
//
// The whole mutation (row, policy, sweep) runs under the blocker's lock, and so
// does an unblock: otherwise an unblock that committed between this handler's
// row and its policy update would leave the policy with a block the database no
// longer has (until the next restart), or the other way round. The server is a
// single replica, so a process-level lock is enough (docs/DESIGN.md, "Blocks").
// Lock order, outermost first: blockLocks[blocker] -> store -> Policy.mu (a leaf,
// see safety.Policy) and blockLocks[blocker] -> Hub.Sweep -> Hub.mu -> actor
// mailboxes. Admission (the actors) and every reader only take Policy's read lock
// and never this one, so nothing below can wait for it.
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	guestID, ok := s.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	if ok, retry := s.blockWrites.Allow("g:" + guestID); !ok {
		ratelimit.Reject(w, retry)
		return
	}
	var req api.BlockRequest
	if !decodeOrFail(w, r, &req) {
		return
	}
	if !safety.ValidBlockTarget(guestID, req.PlayerID) {
		writeError(w, http.StatusBadRequest, api.ErrCodeInvalidBlock)
		return
	}

	defer s.blockLocks.Lock(guestID)()
	// The write and the sweep are not abandoned when the client goes away: a
	// block that is stored must also be enforced. The deadline starts once the
	// lock is held, so waiting for another request of the same blocker does not
	// eat it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), safetyTimeout)
	defer cancel()
	stored, _, err := s.moderation.AddBlock(ctx, safety.Block{
		Blocker: guestID, Blocked: req.PlayerID, BlockedName: cleanBlockName(req.Name), CreatedAt: s.now().UTC(),
	}, safety.MaxBlocksPerGuest)
	switch {
	case errors.Is(err, store.ErrBlockLimit):
		writeError(w, http.StatusConflict, api.ErrCodeBlockLimit)
		return
	case errors.Is(err, store.ErrInvalidBlock):
		writeError(w, http.StatusBadRequest, api.ErrCodeInvalidBlock)
		return
	case err != nil:
		s.log.Warn("storing a block failed", "err", err)
		retryable(w)
		return
	}
	s.policy.AddBlock(guestID, req.PlayerID)

	// Also on a replay (the block existed): a retry after a failed sweep must
	// finish the job.
	removed, err := s.rooms.(policySweeper).Sweep(ctx)
	if err != nil {
		s.log.Warn("sweeping the rooms after a block failed", "err", err)
		retryable(w)
		return
	}
	left := []string{}
	for code, ids := range removed {
		if slices.Contains(ids, guestID) {
			left = append(left, code)
		}
	}
	slices.Sort(left)
	writeJSON(w, http.StatusOK, api.BlockResponse{Block: blockedPlayer(stored), LeftRooms: left})
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	guestID, ok := s.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if ok, retry := s.blockWrites.Allow("g:" + guestID); !ok {
		ratelimit.Reject(w, retry)
		return
	}
	target := r.PathValue("playerId")
	if !safety.ValidToken(target) {
		writeError(w, http.StatusBadRequest, api.ErrCodeInvalidBlock)
		return
	}
	// Under the blocker's lock, like handleBlock (see there for the lock order).
	defer s.blockLocks.Lock(guestID)()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), safetyTimeout)
	defer cancel()
	if _, err := s.moderation.RemoveBlock(ctx, guestID, target); err != nil {
		s.log.Warn("removing a block failed", "err", err)
		retryable(w)
		return
	}
	// An unblock only lifts the refusal: nobody is put back in a room.
	s.policy.RemoveBlock(guestID, target)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	guestID, ok := s.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), safetyTimeout)
	defer cancel()
	blocks, err := s.moderation.ListBlocks(ctx, guestID)
	if err != nil {
		s.log.Warn("listing blocks failed", "err", err)
		retryable(w)
		return
	}
	out := make([]api.BlockedPlayer, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockedPlayer(b))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.BlocksResponse{Blocks: out})
}

// callerOf returns the guest id of the request when the cookie is valid, "" for
// an anonymous caller. It never mints a cookie.
func (s *Server) callerOf(r *http.Request) string {
	if s.guests == nil {
		return ""
	}
	id, _ := s.guests.Identify(r)
	return id
}
