package room

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

const (
	visibilityPrivate = "private"
	visibilityPublic  = "public"
)

// ErrPublicUnavailable: the operator switched public games off
// (SetPublicEnabled), so a public room cannot be created. It maps to 403
// public_unavailable: a decision, not a failure, so never a 5xx.
var ErrPublicUnavailable = errors.New("room: public games are switched off")

// The operator's switch of public games (docs/MODERATION.md, "Absence"), for an
// owner who cannot moderate for a while. While it is off:
//
//   - Create refuses a public room (under h.mu, where the switch is written);
//   - the directory is empty (publicRooms) and GET /api/rooms says it is paused;
//   - a public room admits only the players already seated in it (onJoin, via
//     refusesJoin, in the actor's serialized join path): they reconnect and
//     finish their game, nobody new comes in, by the directory or by code;
//   - no NEW automated opponent is seated (reconcileBots, botFillPending): a
//     pending addition is dropped, the opponents already seated keep playing;
//   - private rooms are untouched.
//
// It lives in memory: Config.PublicDisabled (PUBLIC_ROOMS_ENABLED) is what a
// restart comes back to.

// PublicEnabled reports whether public games are on.
func (h *Hub) PublicEnabled() bool { return !h.publicOff.Load() }

// ErrPublicRoomsPending wraps the context's error when SetPublicEnabled wrote
// the switch (it is in force) but not every live room re-evaluated its
// automated opponents before the context ended.
var ErrPublicRoomsPending = errors.New("room: the switch of public games is in force, but not every room applied it yet")

// SetPublicEnabled turns public games on or off. When ctx is already done it
// changes nothing and returns ctx's error. Otherwise it writes the switch
// (in force from then on, whatever happens to ctx), then asks every live
// public room to re-evaluate its automated opponents (off: a pending addition
// is dropped; on: a lonely host's lobby is filled again, counting from now)
// and returns once they all did, or ErrPublicRoomsPending when ctx ends first.
func (h *Hub) SetPublicEnabled(ctx context.Context, on bool) error {
	h.mu.Lock()
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return err
	}
	h.publicOff.Store(!on)
	h.m.publicEnabled.Set(boolGauge(on))
	var rooms []*room
	if !h.shuttingDown {
		rooms = slices.Collect(maps.Values(h.rooms))
	}
	h.mu.Unlock()

	done := make(chan struct{}, len(rooms)) // buffered: a late reply must not block an actor
	for _, r := range rooms {
		r.post(func(r *room) {
			r.onPublicSwitch()
			done <- struct{}{}
		}, func() { done <- struct{}{} })
	}
	for range rooms {
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrPublicRoomsPending, ctx.Err())
		}
	}
	return nil
}

// onPublicSwitch runs on the room's goroutine after the switch was flipped.
func (r *room) onPublicSwitch() {
	if r.closed || r.stopping || r.visibility != visibilityPublic {
		return
	}
	now := r.clock.Now()
	prev := r.g.Phase
	if events, changed := r.reconcileBots(now); len(events) > 0 || changed {
		r.commit(now, prev, events, true)
		return
	}
	r.rearm()
}

// publicClosedTo reports whether guestID may not come into this room because
// it is public, public games are off and the guest holds no seat here.
func (r *room) publicClosedTo(guestID string) bool {
	return r.visibility == visibilityPublic && r.h.publicOff.Load() &&
		!slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.ID == guestID && !p.Left })
}

func boolGauge(on bool) float64 {
	if on {
		return 1
	}
	return 0
}

func normalizeVisibility(value string) (string, error) {
	switch value {
	case "", visibilityPrivate:
		return visibilityPrivate, nil
	case visibilityPublic:
		return visibilityPublic, nil
	default:
		return "", fmt.Errorf("%w: unknown visibility %q", game.ErrInvalidSettings, value)
	}
}

type publicEntry struct {
	room      api.PublicRoom
	createdAt time.Time
	// members are the human seats of the lobby: a private index that only the
	// block filter of PublicRoomsFor reads, never part of a response.
	members []string
}

var _ PublicRoomLister = (*Hub)(nil)

// PublicRooms only reads actor-published values, never live games or mailboxes.
// Each response owns its labels, so a caller cannot mutate the directory.
func (h *Hub) PublicRooms(lang game.Lang) []api.PublicRoom {
	rooms, _ := h.publicRooms(lang, nil)
	return rooms
}

// publicRooms lists the directory; keep, when set, drops the entries it
// refuses (the caller's blocks, see PublicRoomsFor). It also returns whether
// public games are on, read in the same critical section as the directory and
// under the mutex SetPublicEnabled writes the switch with: the list is empty
// whenever enabled is false, never a stale list next to a fresh "paused".
func (h *Hub) publicRooms(lang game.Lang, keep func(publicEntry) bool) (rooms []api.PublicRoom, enabled bool) {
	h.mu.Lock()
	enabled = !h.publicOff.Load()
	entries := make([]publicEntry, 0, len(h.directory))
	if !h.shuttingDown && enabled {
		for _, entry := range h.directory {
			if (lang == "" || entry.room.Lang == lang) && (keep == nil || keep(entry)) {
				entries = append(entries, entry)
			}
		}
	}
	h.mu.Unlock()
	slices.SortFunc(entries, func(a, b publicEntry) int {
		if n := a.createdAt.Compare(b.createdAt); n != 0 {
			return n
		}
		return cmp.Compare(a.room.Code, b.room.Code)
	})
	result := make([]api.PublicRoom, 0, min(len(entries), 50))
	for _, entry := range entries[:min(len(entries), 50)] {
		entry.room.CategoryLabels = slices.Clone(entry.room.CategoryLabels)
		result = append(result, entry.room)
	}
	return result, enabled
}

func (r *room) publishPublic() {
	host := r.players[r.g.HostID]
	eligible := r.visibility == visibilityPublic && r.claimed && !r.closed && !r.stopping &&
		r.g.Phase == game.PhaseLobby && r.seated() < game.MaxPlayers &&
		host != nil && !host.dead && !r.g.IsAutomated(r.g.HostID)
	var entry publicEntry
	if eligible {
		labels, custom := publicLabels(r.g.Settings)
		entry = publicEntry{createdAt: r.createdAt, room: api.PublicRoom{
			Code: r.code, Lang: r.g.Settings.Lang, Players: r.seated(), MaxPlayers: game.MaxPlayers,
			CategoryLabels: labels, CustomCategories: custom, Rounds: r.g.Settings.Rounds,
			TimeLimitSeconds: r.g.Settings.TimeLimitSeconds,
		}, members: r.seatedHumans()}
	}
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	if eligible && !r.h.shuttingDown && r.h.rooms[r.code] == r {
		r.h.directory[r.code] = entry
	} else {
		delete(r.h.directory, r.code)
	}
	visible, humans, automated := 0, 0, 0
	if r.visibility == visibilityPublic && !r.closed && !r.stopping && !r.h.shuttingDown {
		if eligible {
			visible = 1
		}
		humans = r.humanConnected()
		for _, p := range r.g.Players {
			if !p.Left && p.Connected && r.g.IsAutomated(p.ID) {
				automated++
			}
		}
	}
	r.h.m.publicPopulation(visible-r.publicVisibleReported, humans-r.publicHumansReported, automated-r.publicBotsReported)
	r.publicVisibleReported, r.publicHumansReported, r.publicBotsReported = visible, humans, automated
}
