package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// persistedVersion is the schema version of Persisted. A snapshot with another
// version is skipped (and deleted) on restore.
const persistedVersion = 1

// Persisted is the room-layer schema of a store.Snapshot's State.
type Persisted struct {
	// Version is persistedVersion.
	Version int `json:"version"`
	// Code is the room code (it must match the snapshot's Code).
	Code string `json:"code"`
	// CreatedAt is when the room was created.
	CreatedAt time.Time `json:"createdAt"`
	// LastActive is the last time somebody was connected to the room.
	LastActive time.Time `json:"lastActive"`
	// Game is the JSON of the *game.Game.
	Game json.RawMessage `json:"game"`
	// Users links player ids to the accounts they were signed in with. It is
	// absent for rooms without signed-in players and in snapshots written
	// before accounts existed.
	Users map[string]string `json:"users,omitempty"`
	// GameID identifies the game in progress (minted when it starts, kept in
	// every snapshot until the rematch), so that a game replayed after a crash
	// is recorded under the same id and counts once. Empty in the lobby.
	GameID string `json:"gameId,omitempty"`
	// Pending is the outbox: the finished games of this room whose statistics
	// the store has not confirmed yet, oldest first (at most maxPendingRecords).
	// A restart offers them again (recording is idempotent).
	Pending []store.GameRecord `json:"pending,omitempty"`
	// Outbox is outboxVersion in snapshots written by a server that keeps the
	// outbox; it is absent in older ones (a final-phase snapshot with a game id
	// and no outbox marker records its game again, see restoreStats).
	Outbox int `json:"outbox,omitempty"`

	// droppedPending counts the pending records decodePersisted discarded
	// (unreadable, duplicated or beyond the bound).
	droppedPending int
}

const (
	// outboxVersion marks the snapshots that carry the outbox.
	outboxVersion = 1
	// maxPendingRecords bounds a room's outbox. A room finishes a game every few
	// minutes at best, so it only fills up when the database stays down for hours;
	// the oldest record is then given up (and counted as dropped).
	maxPendingRecords = 8
)

func encodePersisted(code string, createdAt, lastActive time.Time, g *game.Game) ([]byte, error) {
	return encodeRoomState(Persisted{Code: code, CreatedAt: createdAt, LastActive: lastActive}, g)
}

// encodeRoomState marshals p with the game g (the Version and Game fields are
// filled in here).
func encodeRoomState(p Persisted, g *game.Game) ([]byte, error) {
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("marshal game: %w", err)
	}
	p.Version = persistedVersion
	p.Game = raw
	return json.Marshal(p)
}

// decodePersisted parses and sanity-checks a snapshot.
func decodePersisted(snap store.Snapshot) (Persisted, *game.Game, error) {
	var p Persisted
	if err := json.Unmarshal(snap.State, &p); err != nil {
		return p, nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if p.Version != persistedVersion {
		return p, nil, fmt.Errorf("unsupported snapshot version %d", p.Version)
	}
	if p.Code != snap.Code || !validCode(p.Code) {
		return p, nil, fmt.Errorf("snapshot code %q does not match %q", p.Code, snap.Code)
	}
	var g game.Game
	if err := json.Unmarshal(p.Game, &g); err != nil {
		return p, nil, fmt.Errorf("decode game: %w", err)
	}
	if err := sanitize(&g); err != nil {
		return p, nil, err
	}
	p.Users, p.GameID = cleanLinks(&g, p.Users, p.GameID)
	p.Pending, p.droppedPending = cleanPending(p.Pending)
	return p, &g, nil
}

// cleanPending keeps the outbox records the store could accept, without
// repeats, and at most maxPendingRecords (the newest). It reports how many it
// discarded. A snapshot is trusted for its structure, not for these: they feed
// the statistics.
func cleanPending(pending []store.GameRecord) ([]store.GameRecord, int) {
	var kept []store.GameRecord
	dropped := 0
	for _, rec := range pending {
		if rec.Validate() != nil || slices.ContainsFunc(kept, func(k store.GameRecord) bool { return k.ID == rec.ID }) {
			dropped++
			continue
		}
		kept = append(kept, rec)
	}
	if extra := len(kept) - maxPendingRecords; extra > 0 {
		kept = kept[extra:]
		dropped += extra
	}
	return kept, dropped
}

// cleanLinks keeps the account links of the seats that exist (with a
// well-formed user id) and the id of the game in progress (any phase but the
// lobby). A snapshot is trusted for its structure but not for these: they feed
// the statistics.
func cleanLinks(g *game.Game, users map[string]string, gameID string) (map[string]string, string) {
	var clean map[string]string
	for id, userID := range users {
		if !store.ValidID(userID) || !slices.ContainsFunc(g.Players, func(p game.Player) bool { return p.ID == id }) {
			continue
		}
		if clean == nil {
			clean = map[string]string{}
		}
		clean[id] = userID
	}
	if g.Phase == game.PhaseLobby || !store.ValidID(gameID) {
		gameID = ""
	}
	return clean, gameID
}

// sanitize fixes what a JSON round trip loses (nil maps) and rejects states
// the engine could not have produced, which would make it panic.
func sanitize(g *game.Game) error {
	switch g.Phase {
	case game.PhaseLobby, game.PhaseLetter, game.PhasePlaying, game.PhaseGrace,
		game.PhaseReview, game.PhaseRoundSummary, game.PhaseFinal:
	default:
		return fmt.Errorf("unknown phase %q", g.Phase)
	}
	if len(g.Players) == 0 {
		return errors.New("game has no players")
	}
	if !g.Settings.Lang.Valid() {
		return fmt.Errorf("unknown language %q", g.Settings.Lang)
	}
	if g.Round == nil {
		if g.Phase != game.PhaseLobby && g.Phase != game.PhaseFinal {
			return fmt.Errorf("phase %s without a round", g.Phase)
		}
		return nil
	}
	if g.Round.Answers == nil {
		g.Round.Answers = map[string][]string{}
	}
	for _, id := range g.Round.Participants {
		if len(g.Round.Answers[id]) != len(g.Settings.Categories) {
			return fmt.Errorf("answers of %q do not match the categories", id)
		}
	}
	return nil
}

// Tunables of the saver. They are fields of the saver so tests can shrink
// them; the defaults are what production uses.
const (
	defaultSaveTimeout = 10 * time.Second
	defaultMinBackoff  = 200 * time.Millisecond
	defaultMaxBackoff  = 10 * time.Second
)

// saver persists one room's snapshots in its own goroutine so the room's
// actor never waits for the store. It is latest-wins: a snapshot submitted
// while an older one is still being written (or waiting to be retried)
// replaces it, so a slow or failing store cannot build up a backlog. Writes
// and the final deletion are serialised, which guarantees an older snapshot
// never overwrites a newer one and a deleted room is not resurrected.
type saver struct {
	code  string
	store store.Store
	clock Clock
	log   *slog.Logger
	// ctx is the hub's base context: cancelled at the very end of Shutdown to
	// abort writes that hang.
	ctx context.Context
	// quit is closed when the hub begins to shut down: it ends the retries of
	// a snapshot deletion (a save must still be retried: the final snapshot
	// depends on it).
	quit       <-chan struct{}
	m          *metrics
	onError    func()
	onStop     func()
	timeout    time.Duration
	minBackoff time.Duration
	maxBackoff time.Duration

	kick    chan struct{}
	ctrl    chan struct{} // closed by finish
	stopped chan struct{} // closed when run has returned

	mu       sync.Mutex
	latest   []byte // newest snapshot not yet written; nil when none
	inflight bool   // a write (or its retry) of a taken snapshot is under way
	queued   bool   // counted in the snapshot_save_queue gauge
	seq      uint64 // sequence number of the newest submitted snapshot
	done     uint64 // every snapshot up to this sequence number is persisted
	finished bool
	del      bool
	dead     bool
	changed  chan struct{} // closed (and replaced) whenever done or dead changes
}

func newSaver(h *Hub, code string) *saver {
	return &saver{
		code:       code,
		store:      h.store,
		clock:      h.clock,
		log:        h.log.With("room", code),
		ctx:        h.baseCtx,
		quit:       h.quit,
		m:          h.m,
		onError:    h.m.snapshotErrors.Inc,
		onStop:     func() { h.unretire(code) },
		timeout:    h.saveTimeout,
		minBackoff: h.minBackoff,
		maxBackoff: h.maxBackoff,
		kick:       make(chan struct{}, 1),
		ctrl:       make(chan struct{}),
		stopped:    make(chan struct{}),
		changed:    make(chan struct{}),
	}
}

// submit hands over the newest snapshot and returns its sequence number. It
// never blocks. After finish it is a no-op.
func (s *saver) submit(data []byte) uint64 {
	s.mu.Lock()
	if s.finished {
		seq := s.seq
		s.mu.Unlock()
		return seq
	}
	s.seq++
	seq := s.seq
	s.latest = data
	s.syncQueueLocked()
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return seq
}

// flush waits until the snapshot with sequence number seq (or a newer one) is
// persisted, the saver stops, or ctx ends.
func (s *saver) flush(ctx context.Context, seq uint64) error {
	for {
		s.mu.Lock()
		done, dead, changed := s.done, s.dead, s.changed
		s.mu.Unlock()
		if done >= seq {
			return nil
		}
		if dead {
			return errSaverStopped
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var errSaverStopped = errors.New("room: snapshot saver stopped")

// finish stops the saver: with del it discards anything not yet written and
// deletes the room's snapshot; without it, it just stops. It never blocks.
func (s *saver) finish(del bool) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	s.del = del
	if del {
		s.latest = nil
		s.syncQueueLocked()
	}
	s.mu.Unlock()
	close(s.ctrl)
}

// syncQueueLocked keeps the snapshot_save_queue gauge equal to the number of
// savers that have a snapshot to write: one waiting for its turn, being
// written, or waiting for a retry. s.mu must be held.
func (s *saver) syncQueueLocked() {
	want := s.latest != nil || s.inflight
	if want == s.queued {
		return
	}
	s.queued = want
	if want {
		s.m.snapshotQueue.Inc()
	} else {
		s.m.snapshotQueue.Dec()
	}
}

func (s *saver) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *saver) run() {
	removed := false
	defer func() {
		s.mu.Lock()
		s.dead = true
		s.latest, s.inflight = nil, false
		s.syncQueueLocked()
		// A room whose snapshot may still be in the store keeps its code
		// reserved: a new room with the same code would otherwise have its
		// snapshot deleted by a late retry, or be replaced by the old room
		// after a restart.
		release := !s.del || removed
		s.broadcastLocked()
		s.mu.Unlock()
		close(s.stopped)
		if release {
			s.onStop()
		}
	}()
	backoff := s.minBackoff
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		finished, del := s.finished, s.del
		data, seq := s.latest, s.seq
		s.latest = nil
		s.inflight = data != nil && !finished
		s.syncQueueLocked()
		s.mu.Unlock()

		switch {
		case finished && del:
			removed = s.remove()
			return
		case finished:
			return
		case data == nil:
			select {
			case <-s.kick:
			case <-s.ctrl:
			case <-s.ctx.Done():
			}
			continue
		}

		began := s.clock.Now()
		err := s.write(data)
		took := s.clock.Now().Sub(began)
		if err == nil {
			s.m.snapshotSaved(took, s.clock.Now())
			backoff = s.minBackoff
			s.mu.Lock()
			s.done = max(s.done, seq)
			s.inflight = false
			s.syncQueueLocked()
			s.broadcastLocked()
			s.mu.Unlock()
			continue
		}
		if s.ctx.Err() != nil {
			return
		}
		s.m.snapshotWriteFailed(took)
		s.onError()
		s.log.Warn("saving room snapshot failed", "err", err, "retry_in", backoff)
		s.mu.Lock()
		if s.latest == nil && !s.del {
			s.latest = data // retry the same snapshot unless a newer one arrived
		}
		s.inflight = false
		s.syncQueueLocked()
		s.mu.Unlock()
		s.sleep(backoff, s.ctrl)
		backoff = min(backoff*2, s.maxBackoff)
	}
}

func (s *saver) write(data []byte) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
	defer cancel()
	return s.store.Save(ctx, store.Snapshot{Code: s.code, State: data, UpdatedAt: s.clock.Now()})
}

// remove deletes the snapshot of a closed room and reports whether it did. A
// failure is retried with capped exponential backoff, without a limit, until
// it succeeds: only then is the room's code released for reuse. It gives up
// (reporting false, the code stays reserved) when the hub begins to shut down
// or is torn down: the process is exiting, and a restart that finds the
// stale snapshot just restores a room that closes again on its own.
func (s *saver) remove() bool {
	backoff := s.minBackoff
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
		err := s.store.Delete(ctx, s.code)
		cancel()
		switch {
		case err == nil:
			s.m.snapshotsDeleted.WithLabelValues(deletedRoomClosed).Inc()
			return true
		case s.ctx.Err() != nil:
			return false
		}
		s.onError()
		s.log.Warn("deleting room snapshot failed; its code stays reserved until it succeeds",
			"err", err, "attempt", attempt, "retry_in", backoff)
		if !s.sleep(backoff, s.quit) {
			return false
		}
		backoff = min(backoff*2, s.maxBackoff)
	}
}

// sleep waits d on the hub's clock. It reports false when it was cut short:
// by stop being closed or by the hub's teardown.
func (s *saver) sleep(d time.Duration, stop <-chan struct{}) bool {
	fired := make(chan struct{}, 1)
	t := s.clock.AfterFunc(d, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	defer t.Stop()
	select {
	case <-fired:
		return true
	case <-stop:
		return false
	case <-s.ctx.Done():
		return false
	}
}
