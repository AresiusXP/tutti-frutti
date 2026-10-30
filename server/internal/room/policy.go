package room

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// This file is the room side of blocking and moderation restrictions
// (docs/DESIGN.md, "Safety").
//
// The policy (safety.Policy) is an in-memory index owned by the process. The
// room actors only ever READ it, inside their own serialized tasks:
//
//   - Admission: onJoin consults it before the engine runs (so before any
//     automated seat is replaced), for new seats and for reclaimed ones alike.
//   - Sweep: whoever changes the policy (the REST handler of a block, the
//     operator's restrict command) updates it AFTER the database write
//     succeeded and then calls Hub.Sweep, which posts one task to every live
//     room. The task removes, through the normal Leave path, whoever the policy
//     now keeps from sharing the room, and the call returns once every actor
//     has run it.
//
// Why a join racing with a change cannot leave a forbidden pair seated: the
// policy change happens before the sweep tasks are posted, and a task posted
// later runs later in each actor's mailbox. A join J processed by room R is
// ordered against R's sweep task S: if J runs after S it reads the new policy
// and is refused; if J runs before S, S finds whatever J seated and removes it.
// A room created while the sweep is being posted is either in the snapshot of
// rooms the sweep takes (and gets its own task) or was spawned after it, and
// then every join to it runs after the policy change. A restored room sweeps
// itself in onInit, with the policy loaded before Hub.Start.

// ErrRestricted: the guest (or the account) is under a moderation restriction
// and cannot create a room. It maps to 403 join_unavailable and, like the
// refusal of a join, never says why.
var ErrRestricted = errors.New("room: restricted")

// seatedHumans returns the ids of the human seats that did not leave.
func (r *room) seatedHumans() []string {
	var ids []string
	for _, p := range r.g.Players {
		if !p.Left && !r.g.IsAutomated(p.ID) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// refusesJoin reports whether the guest may not take or keep a seat here: it
// (or its account) is restricted, or it shares a block, in either direction,
// with a human who is seated in this room, or the room is public, public games
// are switched off and the guest has no seat in it (public.go). It runs on the
// room's goroutine.
func (r *room) refusesJoin(guestID, userID string) bool {
	if r.publicClosedTo(guestID) {
		return true
	}
	p := r.h.cfg.Policy
	if p == nil {
		return false
	}
	if p.Restricted(guestID) || p.RestrictedUser(userID) {
		return true
	}
	for _, other := range r.seatedHumans() {
		if other != guestID && p.Conflict(guestID, other) {
			return true
		}
	}
	return false
}

// rejectJoinUnavailable answers a refused join with the generic code: it does
// not say who or why.
func (r *room) rejectJoinUnavailable(s *session) {
	r.reject(s, game.CmdJoin, api.ErrCodeJoinUnavailable)
}

// violator returns a seated human the policy no longer allows in this room, or
// "": a restricted guest or account first, then the blocker of a pair that
// shares the room (a block is "block and leave": the blocker goes).
func (r *room) violator(p *safety.Policy) string {
	humans := r.seatedHumans()
	for _, id := range humans {
		if p.Restricted(id) || p.RestrictedUser(r.accounts[id]) {
			return id
		}
	}
	for _, a := range humans {
		for _, b := range humans {
			if a != b && p.Blocks(a, b) {
				return a
			}
		}
	}
	return ""
}

// enforcePolicy removes from the room, one by one and through the normal Leave
// path (the same one a player's own "leave" takes: a lobby seat goes, a seat of
// a started game is flagged Left and keeps its score), everybody the policy no
// longer lets share it. It returns the removed players' ids. It is idempotent
// and runs on the room's goroutine.
func (r *room) enforcePolicy() []string {
	p := r.h.cfg.Policy
	if p == nil || r.closed || r.stopping {
		return nil
	}
	var removed []string
	for range game.MaxPlayers {
		id := r.violator(p)
		if id == "" || r.closed || !r.evict(id) {
			break
		}
		removed = append(removed, id)
	}
	return removed
}

// evict applies the trusted Leave to a seat, closes its connection like a
// "leave" does (CloseLeft) and tells the others.
func (r *room) evict(id string) bool {
	now := r.clock.Now()
	prev := r.g.Phase
	events, err := r.g.Apply(now, id, game.Leave{})
	if err != nil {
		r.log.Warn("removing a player the policy no longer allows failed", "player", id, "err", err)
		return false
	}
	s := r.players[id]
	if s != nil {
		r.detach(s)
	}
	r.log.Info("a player was removed from the room by the safety policy")
	r.commit(now, prev, events, true)
	if s != nil {
		s.c.Close(protocol.CloseLeft, "left")
	}
	return true
}

// Sweep asks every live room to re-evaluate itself against the policy and
// returns, per room code, the players it removed. Call it after changing the
// policy (and after the change reached the database): see the argument at the
// top of this file. It returns once every room has done so, or with ctx's
// error; ErrShuttingDown while the hub drains.
func (h *Hub) Sweep(ctx context.Context) (map[string][]string, error) {
	h.mu.Lock()
	if h.shuttingDown {
		h.mu.Unlock()
		return nil, ErrShuttingDown
	}
	rooms := slices.Collect(maps.Values(h.rooms))
	h.mu.Unlock()

	type swept struct {
		code    string
		removed []string
	}
	ch := make(chan swept, len(rooms)) // buffered: a reply after ctx ended must not block an actor
	for _, r := range rooms {
		r.post(func(r *room) { ch <- swept{r.code, r.enforcePolicy()} }, func() { ch <- swept{} })
	}
	out := map[string][]string{}
	for range rooms {
		select {
		case s := <-ch:
			if len(s.removed) > 0 {
				out[s.code] = s.removed
			}
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
	return out, nil
}

// InfoFor is Info for a caller that is identified (by its guest cookie): a room
// that holds a human who shares a block with the caller, in either direction, is
// reported as not joinable, exactly like a full one (no other reason is given,
// and no roster is revealed). A restricted caller finds no joinable room.
// guestID "" is an anonymous caller (Info): blocks do not apply to it. For
// every caller a public room is not joinable while public games are switched
// off, unless the caller holds a seat in it (public.go).
func (h *Hub) InfoFor(code, guestID string) (Info, bool) {
	h.mu.Lock()
	r := h.rooms[normalizeCode(code)]
	shuttingDown := h.shuttingDown
	h.mu.Unlock()
	if r == nil {
		return Info{}, false
	}
	type answer struct {
		info Info
		ok   bool
	}
	ch := make(chan answer, 1)
	r.post(func(r *room) {
		info := r.summary()
		if r.publicClosedTo(guestID) || guestID != "" && r.refusesJoin(guestID, "") {
			info.Joinable = false
		}
		ch <- answer{info, true}
	}, func() { ch <- answer{} })
	a := <-ch
	if !a.ok {
		return Info{}, false
	}
	a.info.Joinable = a.info.Joinable && !shuttingDown
	return a.info, true
}

// PublicRoomsFor is PublicRooms for an identified caller: it leaves out the
// waiting lobbies that hold a human who shares a block with the caller. The
// membership it consults is a private field of the actor-published directory
// entry; it never leaves this function. guestID "" lists everything.
func (h *Hub) PublicRoomsFor(lang game.Lang, guestID string) []api.PublicRoom {
	rooms, _ := h.PublicDirectory(lang, guestID)
	return rooms
}

// PublicDirectory is PublicRoomsFor plus whether public games are on, both
// from one consistent snapshot (see publicRooms): GET /api/rooms answers
// "paused" from it, so the flag and the list cannot disagree.
func (h *Hub) PublicDirectory(lang game.Lang, guestID string) (rooms []api.PublicRoom, enabled bool) {
	return h.publicRooms(lang, func(e publicEntry) bool {
		p := h.cfg.Policy
		if p == nil || guestID == "" {
			return true
		}
		if p.Restricted(guestID) {
			return false
		}
		return !slices.ContainsFunc(e.members, func(m string) bool { return p.Conflict(guestID, m) })
	})
}

// CloseTarget names the room instance the operator wants closed: by code, by
// instance id, or both (then the room that has the code must be that instance).
type CloseTarget struct {
	Code       string
	InstanceID string
}

// CloseResult says what was closed.
type CloseResult struct {
	Code           string
	RoomInstanceID string
}

// CloseRoom closes a room as an operator takedown: every client gets the usual
// "room closed" (close code 4004) and the snapshot is deleted, exactly like a
// room that ended by itself. It runs through the room's own goroutine, never
// from the caller's. With a code and an instance id the room that holds the code
// must be that instance (ErrInstanceMismatch otherwise: the code was reused);
// with only an instance id every live room is asked. ErrRoomNotFound when
// nothing matches.
func (h *Hub) CloseRoom(ctx context.Context, t CloseTarget) (CloseResult, error) {
	if t.Code == "" && t.InstanceID == "" {
		return CloseResult{}, ErrRoomNotFound
	}
	h.mu.Lock()
	if h.shuttingDown {
		h.mu.Unlock()
		return CloseResult{}, ErrShuttingDown
	}
	var rooms []*room
	if t.Code != "" {
		if r := h.rooms[normalizeCode(t.Code)]; r != nil {
			rooms = append(rooms, r)
		}
	} else {
		rooms = slices.Collect(maps.Values(h.rooms))
	}
	h.mu.Unlock()
	if len(rooms) == 0 {
		return CloseResult{}, ErrRoomNotFound
	}

	type closed struct {
		res CloseResult
		err error
	}
	ch := make(chan closed, len(rooms))
	for _, r := range rooms {
		r.post(func(r *room) {
			switch {
			case r.closed || r.stopping:
				ch <- closed{err: ErrRoomNotFound}
			case t.InstanceID != "" && r.instanceID != t.InstanceID:
				if t.Code != "" {
					ch <- closed{err: ErrInstanceMismatch}
				} else {
					ch <- closed{err: ErrRoomNotFound}
				}
			default:
				res := CloseResult{Code: r.code, RoomInstanceID: r.instanceID}
				r.log.Info("room closed by the operator")
				r.closeRoom(protocol.CloseRoomClosed, "room closed", true)
				ch <- closed{res: res}
			}
		}, func() { ch <- closed{err: ErrRoomNotFound} })
	}
	var firstErr error
	for range rooms {
		select {
		case c := <-ch:
			if c.err == nil {
				return c.res, nil
			}
			if firstErr == nil || errors.Is(c.err, ErrInstanceMismatch) {
				firstErr = c.err
			}
		case <-ctx.Done():
			return CloseResult{}, ctx.Err()
		}
	}
	return CloseResult{}, firstErr
}
