package room

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

const (
	visibilityPrivate = "private"
	visibilityPublic  = "public"
)

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
}

var _ PublicRoomLister = (*Hub)(nil)

// PublicRooms only reads actor-published values, never live games or mailboxes.
// Each response owns its labels, so a caller cannot mutate the directory.
func (h *Hub) PublicRooms(lang game.Lang) []api.PublicRoom {
	h.mu.Lock()
	entries := make([]publicEntry, 0, len(h.directory))
	if !h.shuttingDown {
		for _, entry := range h.directory {
			if lang == "" || entry.room.Lang == lang {
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
	return result
}

func (r *room) publishPublic() {
	host := r.players[r.g.HostID]
	eligible := r.visibility == visibilityPublic && r.claimed && !r.closed && !r.stopping &&
		r.g.Phase == game.PhaseLobby && r.seated() < game.MaxPlayers &&
		host != nil && !host.dead && !r.g.IsAutomated(r.g.HostID)
	var entry publicEntry
	if eligible {
		entry = publicEntry{createdAt: r.createdAt, room: api.PublicRoom{
			Code: r.code, Lang: r.g.Settings.Lang, Players: r.seated(), MaxPlayers: game.MaxPlayers,
			CategoryLabels: r.g.Settings.Labels(), Rounds: r.g.Settings.Rounds,
			TimeLimitSeconds: r.g.Settings.TimeLimitSeconds,
		}}
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
