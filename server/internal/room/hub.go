package room

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Defaults of Config.
const (
	defaultIdleTTL        = 30 * time.Minute
	defaultUnclaimedTTL   = 2 * time.Minute
	defaultJoinTimeout    = 10 * time.Second
	defaultSnapshotEvery  = 5 * time.Second
	defaultSnapshotMaxAge = 6 * time.Hour
	defaultJanitorEvery   = time.Hour
	defaultMaxRooms       = 1000
)

const (
	// codeAlphabet has no vowels (no words) and no look-alikes (0/O, 1/I/L).
	codeAlphabet = "BCDFGHJKLMNPQRSTVWXZ23456789"
	codeLength   = 4
	// maxCodeAttempts bounds the retries when a generated code is taken.
	maxCodeAttempts = 64
)

// Hub is the registry of live rooms: it creates them, hands out their codes,
// restores them from the store on startup, reaps the idle ones and shuts them
// down gracefully. It is safe for concurrent use. Create it with NewHub.
type Hub struct {
	cfg   Config
	log   *slog.Logger
	clock Clock
	store store.Store
	m     *metrics
	// recorder is nil when Config.Recorder is.
	recorder *recorder

	// baseCtx is the context of every store call made in the background; it
	// is cancelled at the very end of Shutdown to abort calls that hang.
	baseCtx    context.Context
	baseCancel context.CancelFunc
	// wg tracks every goroutine of the hub: actors, savers and the janitor.
	wg sync.WaitGroup
	// quit is closed when Shutdown begins.
	quit chan struct{}

	mu           sync.Mutex
	rooms        map[string]*room
	retiring     map[string]*room       // closed rooms whose snapshot may still be deleted
	directory    map[string]publicEntry // immutable actor-published summaries
	shuttingDown bool
	started      bool

	conns     atomic.Int64
	botRandMu sync.Mutex
	idMu      sync.Mutex // serializes Config.IDSource
	// forgotten remembers the accounts this process deleted (see account.go).
	forgotten forgottenAccounts

	shutdownOnce sync.Once
	shutdownErr  error

	// heartbeat is how often a live room refreshes its snapshot even when
	// nothing changed (see heartbeatFor).
	heartbeat time.Duration

	// Saver tunables (tests shrink them before the hub does any work).
	saveTimeout time.Duration
	minBackoff  time.Duration
	maxBackoff  time.Duration
}

var _ Rooms = (*Hub)(nil)

// heartbeatFor is how often a live room saves its snapshot when nothing else
// does: a quarter of SnapshotMaxAge. The janitor deletes the snapshots older
// than SnapshotMaxAge, so with this a live room's snapshot is never older than
// a quarter of that (plus a save in flight), whatever its phase, and the
// janitor cannot reach it even if a few saves fail or are late. (A room that
// is no longer live is closed, and its snapshot deleted, by the room itself.)
func heartbeatFor(maxAge time.Duration) time.Duration {
	return max(maxAge/4, time.Millisecond)
}

// NewHub returns a Hub. Zero fields of cfg take their documented defaults;
// cfg.Store is required. Call Start to restore the saved rooms and start the
// janitor, and Shutdown to stop.
func NewHub(cfg Config) *Hub {
	if cfg.Store == nil {
		panic("room: Config.Store is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = defaultIdleTTL
	}
	if cfg.UnclaimedTTL <= 0 {
		cfg.UnclaimedTTL = defaultUnclaimedTTL
	}
	if cfg.JoinTimeout <= 0 {
		cfg.JoinTimeout = defaultJoinTimeout
	}
	if cfg.SnapshotEvery <= 0 {
		cfg.SnapshotEvery = defaultSnapshotEvery
	}
	if cfg.SnapshotMaxAge <= 0 {
		cfg.SnapshotMaxAge = defaultSnapshotMaxAge
	}
	if cfg.JanitorEvery <= 0 {
		cfg.JanitorEvery = defaultJanitorEvery
	}
	if cfg.MaxRooms <= 0 {
		cfg.MaxRooms = defaultMaxRooms
	}
	if cfg.CodeSource == nil {
		cfg.CodeSource = rand.Reader
	}
	if cfg.BotRand == nil {
		cfg.BotRand = mathrand.IntN
	}
	if cfg.IDSource == nil {
		cfg.IDSource = rand.Reader
	}
	if cfg.MaxBots <= 0 {
		cfg.MaxBots = 2
	}
	cfg.MaxBots = min(cfg.MaxBots, 2)
	if cfg.BotFirstDelayMin <= 0 {
		cfg.BotFirstDelayMin = 20 * time.Second
	}
	if cfg.BotFirstDelayMax <= 0 {
		cfg.BotFirstDelayMax = 30 * time.Second
	}
	if cfg.BotSecondDelayMin <= 0 {
		cfg.BotSecondDelayMin = 40 * time.Second
	}
	if cfg.BotSecondDelayMax <= 0 {
		cfg.BotSecondDelayMax = 55 * time.Second
	}
	cfg.BotFirstDelayMax = max(cfg.BotFirstDelayMin, cfg.BotFirstDelayMax)
	cfg.BotSecondDelayMin = max(cfg.BotFirstDelayMin, cfg.BotSecondDelayMin)
	cfg.BotSecondDelayMax = max(cfg.BotSecondDelayMin, cfg.BotSecondDelayMax)
	log := cfg.Logger.With("component", "room")
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		cfg:         cfg,
		log:         log,
		clock:       cfg.Clock,
		store:       cfg.Store,
		m:           newMetrics(cfg.Registerer, log),
		baseCtx:     ctx,
		baseCancel:  cancel,
		quit:        make(chan struct{}),
		rooms:       map[string]*room{},
		retiring:    map[string]*room{},
		directory:   map[string]publicEntry{},
		heartbeat:   heartbeatFor(cfg.SnapshotMaxAge),
		saveTimeout: defaultSaveTimeout,
		minBackoff:  defaultMinBackoff,
		maxBackoff:  defaultMaxBackoff,
	}
	if cfg.Recorder != nil {
		h.recorder = newRecorder(h, cfg.Recorder)
	}
	return h
}

// Start restores the rooms saved in the store (every player disconnected, the
// idle clock restarted) and starts the janitor, which stops when ctx is done
// or the hub shuts down. It returns the number of rooms restored. It is meant
// to be called once, before the hub serves anything (Create, Connect); a Hub
// that was never started still works (without restore and janitor). A
// snapshot whose code belongs to a room that is already live (or whose code is
// still reserved by a room being closed) is skipped with a warning and left
// alone in the store: Start never replaces a live room.
func (h *Hub) Start(ctx context.Context) (int, error) {
	h.mu.Lock()
	switch {
	case h.shuttingDown:
		h.mu.Unlock()
		return 0, ErrShuttingDown
	case h.started:
		h.mu.Unlock()
		return 0, errors.New("room: hub already started")
	}
	h.started = true
	h.mu.Unlock()

	began := h.clock.Now()
	rooms, err := h.restore(ctx)
	if err != nil {
		return len(rooms), err
	}
	// Return once the restored rooms are up: their idle clock has restarted
	// and their timers are armed.
	for _, r := range rooms {
		ready := make(chan struct{})
		r.post(func(*room) { close(ready) }, func() { close(ready) })
		<-ready
	}
	restored := len(rooms)
	h.m.roomsRestored.Add(float64(restored))
	h.m.restoreDuration.Set(h.clock.Now().Sub(began).Seconds())

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shuttingDown {
		return restored, ErrShuttingDown
	}
	// The first sweep is scheduled before Start returns.
	tick := make(chan struct{}, 1)
	timer := h.armJanitor(tick)
	h.wg.Add(1)
	go h.janitor(ctx, tick, timer)
	return restored, nil
}

// restore rebuilds the rooms of the saved snapshots. A snapshot that cannot be
// decoded, has an unknown version or an impossible state is logged and
// deleted.
func (h *Hub) restore(ctx context.Context) ([]*room, error) {
	snaps, err := h.store.LoadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("room: loading snapshots: %w", err)
	}
	now := h.clock.Now()
	var restored []*room
	for _, snap := range snaps {
		if h.codeInUse(snap.Code) {
			h.log.Warn("not restoring room: its code is already in use; leaving the snapshot alone", "room", snap.Code)
			continue
		}
		p, g, err := decodePersisted(snap)
		if err != nil {
			h.log.Warn("discarding unusable room snapshot", "room", snap.Code, "err", err)
			h.m.restoreErrors.Inc()
			if derr := h.store.Delete(ctx, snap.Code); derr != nil {
				h.log.Warn("deleting unusable room snapshot failed", "room", snap.Code, "err", derr)
			}
			continue
		}
		// An account deleted while the server was down leaves no trace in the room.
		h.forgetDeletedAccounts(ctx, &p)
		// A vote to stop does not survive a restart: with every seat
		// disconnected it would have no voters and pass by itself, and its
		// 15 s are long gone. It is dropped silently, before the seats are
		// marked, so that nobody hears of it.
		if g.Round != nil {
			g.Round.StopVote = nil
			// The same goes for a question to the AI judge: its call died
			// with the process, so the host is let ask again.
			g.Round.JudgePending = nil
		}
		// Nobody has a socket yet: every seat is disconnected until its
		// player reconnects. The first call also applies any deadline that
		// passed while the server was down.
		for i := range g.Players {
			if _, err := g.SetConnected(now, g.Players[i].ID, false); err != nil {
				g.Players[i].Connected = false // a seat that left: just make sure
			}
		}

		h.mu.Lock()
		if h.shuttingDown {
			h.mu.Unlock()
			return restored, ErrShuttingDown
		}
		if h.codeInUseLocked(p.Code) { // a room appeared while the snapshot was decoded
			h.mu.Unlock()
			h.log.Warn("not restoring room: its code is already in use; leaving the snapshot alone", "room", p.Code)
			continue
		}
		if len(h.rooms) >= h.cfg.MaxRooms {
			h.mu.Unlock()
			h.log.Warn("not restoring room: too many rooms", "room", snap.Code, "max", h.cfg.MaxRooms)
			h.m.restoreErrors.Inc()
			continue
		}
		r := h.newRoom(p.Code, g, p.CreatedAt, true)
		r.visibility, r.botSuppressed = p.Visibility, p.BotSuppressed
		r.restoreInstance(p)
		r.restoreStats(p)
		h.spawnLocked(r)
		h.mu.Unlock()
		restored = append(restored, r)
	}
	h.log.Info("restored rooms", "count", len(restored), "snapshots", len(snaps))
	return restored, nil
}

// armJanitor schedules the next sweep.
func (h *Hub) armJanitor(tick chan struct{}) Timer {
	return h.clock.AfterFunc(h.cfg.JanitorEvery, func() {
		select {
		case tick <- struct{}{}:
		default:
		}
	})
}

// janitor deletes the snapshots that were not refreshed for SnapshotMaxAge,
// every JanitorEvery. timer is the first sweep's timer.
func (h *Hub) janitor(ctx context.Context, tick chan struct{}, timer Timer) {
	defer h.wg.Done()
	for {
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return
		case <-h.quit:
			timer.Stop()
			return
		}
		timer = h.armJanitor(tick)
		cutoff := h.clock.Now().Add(-h.cfg.SnapshotMaxAge)
		callCtx, cancel := context.WithTimeout(h.baseCtx, h.saveTimeout)
		n, err := h.store.DeleteOlderThan(callCtx, cutoff)
		cancel()
		switch {
		case err != nil:
			h.m.snapshotErrors.Inc()
			h.log.Warn("snapshot janitor failed", "err", err)
		case n > 0:
			h.m.snapshotsDeleted.WithLabelValues(deletedJanitor).Add(float64(n))
			h.log.Info("snapshot janitor removed stale snapshots", "count", n)
		}
	}
}

// Create implements Rooms.
func (h *Hub) Create(ctx context.Context, req CreateRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	now := h.clock.Now()
	g, err := newGame(req, now)
	if err != nil {
		return "", err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shuttingDown {
		return "", ErrShuttingDown
	}
	// Under h.mu, like the snapshot of rooms Sweep takes: a creation either
	// sees the new restriction or is in the sweep (see policy.go).
	if h.cfg.Policy.Restricted(req.PlayerID) || h.cfg.Policy.RestrictedUser(req.UserID) {
		return "", ErrRestricted
	}
	if len(h.rooms) >= h.cfg.MaxRooms {
		return "", ErrTooManyRooms
	}
	code, err := h.newCodeLocked()
	if err != nil {
		return "", err
	}
	r := h.newRoom(code, g, now, false)
	r.visibility, _ = normalizeVisibility(req.Visibility)
	r.instanceID, r.matchID = h.newInstanceID(), h.newInstanceID()
	h.spawnLocked(r)
	return code, nil
}

// newGame validates the host (the engine is the authority on names, avatars
// and ids) and builds the lobby: the host seat is not connected until the
// host's socket joins.
func newGame(req CreateRequest, now time.Time) (*game.Game, error) {
	visibility, err := normalizeVisibility(req.Visibility)
	if err != nil {
		return nil, err
	}
	name, err := game.CleanName(req.Name)
	if err != nil {
		return nil, err
	}
	// The avatar rules live in the engine's Join; run one on a throwaway
	// lobby to apply them (and the player id check) without duplicating them.
	probe := game.NewGame(game.Player{ID: "\x00" + req.PlayerID, Name: "probe"}, now)
	if _, err := probe.Apply(now, req.PlayerID, game.Join{Name: name, Avatar: req.Avatar}); err != nil {
		return nil, err
	}

	g := game.NewGame(game.Player{ID: req.PlayerID, Name: name, Avatar: req.Avatar}, now)
	if visibility == visibilityPublic {
		g.Settings.TimeLimitSeconds = 90
	}
	if lang := req.Lang; lang.Valid() && lang != g.Settings.Lang {
		s := g.Settings
		s.Lang = lang
		if _, err := g.Apply(now, req.PlayerID, game.UpdateSettings{Settings: s}); err != nil {
			return nil, err
		}
	}
	if _, err := g.SetConnected(now, req.PlayerID, false); err != nil {
		return nil, err
	}
	return g, nil
}

// codeInUse reports whether a live room has the code, or a closed room still
// holds it (its snapshot may not be deleted yet).
func (h *Hub) codeInUse(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.codeInUseLocked(code)
}

func (h *Hub) codeInUseLocked(code string) bool {
	_, live := h.rooms[code]
	_, retiring := h.retiring[code]
	return live || retiring
}

// newCodeLocked returns an unused room code. h.mu must be held.
func (h *Hub) newCodeLocked() (string, error) {
	for range maxCodeAttempts {
		code, err := randomCode(h.cfg.CodeSource)
		if err != nil {
			return "", fmt.Errorf("room: generating a room code: %w", err)
		}
		if !h.codeInUseLocked(code) {
			return code, nil
		}
	}
	return "", ErrTooManyRooms
}

// randomCode draws a code from src with rejection sampling, so that every
// symbol is equally likely (256 is not a multiple of the alphabet size).
func randomCode(src io.Reader) (string, error) {
	const limit = 256 - 256%len(codeAlphabet)
	var b [1]byte
	code := make([]byte, 0, codeLength)
	for len(code) < codeLength {
		if _, err := io.ReadFull(src, b[:]); err != nil {
			return "", err
		}
		if int(b[0]) >= limit {
			continue
		}
		code = append(code, codeAlphabet[int(b[0])%len(codeAlphabet)])
	}
	return string(code), nil
}

// validCode reports whether code looks like a room code.
func validCode(code string) bool {
	if len(code) != codeLength {
		return false
	}
	for i := range len(code) {
		if strings.IndexByte(codeAlphabet, code[i]) < 0 {
			return false
		}
	}
	return true
}

// normalizeCode makes lookups forgiving about case and surrounding spaces.
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// Info implements Rooms.
func (h *Hub) Info(code string) (Info, bool) {
	h.mu.Lock()
	r := h.rooms[normalizeCode(code)]
	shuttingDown := h.shuttingDown
	h.mu.Unlock()
	if r == nil {
		return Info{}, false
	}
	info, ok := r.info()
	if !ok {
		return Info{}, false
	}
	info.Joinable = info.Joinable && !shuttingDown
	return info, true
}

// ShuttingDown reports whether Shutdown has started. While it is true the hub
// no longer knows its rooms (they are being flushed to the store and closed),
// so a missing room does not mean the room does not exist: it will be back
// after the restart. The HTTP layer uses it to answer 503 instead of 404.
func (h *Hub) ShuttingDown() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shuttingDown
}

// Connect implements Rooms.
func (h *Hub) Connect(code string, c Client) (Session, error) {
	h.mu.Lock()
	r := h.rooms[normalizeCode(code)]
	shuttingDown := h.shuttingDown
	h.mu.Unlock()
	switch {
	case shuttingDown:
		return nil, ErrShuttingDown
	case r == nil:
		return nil, ErrRoomNotFound
	}
	return r.connect(c)
}

// Stats implements Rooms.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	rooms := len(h.rooms)
	h.mu.Unlock()
	return Stats{Rooms: rooms, Connections: int(h.conns.Load())}
}

// Shutdown stops the hub gracefully: it refuses new rooms and connections,
// tells every client (event server_restarting), writes a final snapshot of
// every room and waits for it to be persisted, closes every client with
// protocol.CloseServiceRestart, and stops the actors, the savers and the
// janitor. Snapshots are kept so that the next process restores the rooms. It
// waits for all of that until ctx ends and then returns ctx's error; whatever
// is still running is then aborted. Calling it again waits for the first call
// and returns the same result.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.shutdownOnce.Do(func() { h.shutdownErr = h.shutdown(ctx) })
	return h.shutdownErr
}

func (h *Hub) shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.shuttingDown = true
	clear(h.directory)
	rooms := make([]*room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()
	close(h.quit)

	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	// 1. Announce the restart and take the final snapshots inside the actors,
	// which stop accepting frames from now on.
	type final struct {
		seq uint64
		ok  bool
	}
	replies := make([]chan final, len(rooms))
	for i, r := range rooms {
		ch := make(chan final, 1)
		replies[i] = ch
		r.post(func(r *room) {
			seq, ok := r.beginShutdown()
			ch <- final{seq, ok}
		}, func() { ch <- final{} })
	}
	finals := make([]final, len(rooms))
	for i, ch := range replies {
		select {
		case finals[i] = <-ch:
		case <-ctx.Done():
			fail(ctx.Err())
		}
	}

	// The rooms no longer produce results: let the recorder finish what is
	// queued (its goroutine is one of the hub's and is waited for below).
	if h.recorder != nil {
		h.recorder.beginClose()
	}

	// 2. Wait until the final snapshots are in the store.
	for i, f := range finals {
		if !f.ok {
			continue
		}
		if err := rooms[i].saver.flush(ctx, f.seq); err != nil {
			h.log.Warn("final room snapshot not persisted", "room", rooms[i].code, "err", err)
			fail(err)
		}
	}

	// 3. Close the clients and stop the actors, keeping the snapshots.
	for _, r := range rooms {
		r.post(func(r *room) {
			r.closeRoom(protocol.CloseServiceRestart, "server restarting", false)
		}, nil)
	}

	// 4. Wait for every goroutine, then abort whatever is stuck.
	allDone := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(allDone)
	}()
	select {
	case <-allDone:
	case <-ctx.Done():
		fail(ctx.Err())
	}
	h.baseCancel()
	return firstErr
}

// newRoom builds a room (not yet registered). Restored rooms count as already
// claimed: their players have a seat and reconnect with backoff.
func (h *Hub) newRoom(code string, g *game.Game, createdAt time.Time, restored bool) *room {
	r := &room{
		h:          h,
		code:       code,
		log:        h.log.With("room", code),
		clock:      h.clock,
		g:          g,
		createdAt:  createdAt,
		mb:         newMailbox(),
		sessions:   map[*session]struct{}{},
		players:    map[string]*session{},
		claimed:    restored,
		visibility: visibilityPrivate,
	}
	r.saver = newSaver(h, code)
	return r
}

// spawnLocked registers r and starts its goroutines. h.mu must be held (this
// is what keeps wg.Add from racing with Shutdown's Wait).
func (h *Hub) spawnLocked(r *room) {
	h.rooms[r.code] = r
	h.m.rooms.Inc()
	r.post(func(r *room) { r.onInit() }, nil)
	h.wg.Add(2)
	go func() {
		defer h.wg.Done()
		r.saver.run()
	}()
	go func() {
		defer h.wg.Done()
		r.loop()
	}()
}

// retire removes a closed room from the registry. Its code stays reserved
// until its snapshot has actually been deleted (or the room was kept for a
// restart), so a new room cannot reuse a code whose old row is still in the
// store.
func (h *Hub) retire(r *room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[r.code] == r {
		delete(h.rooms, r.code)
		delete(h.directory, r.code)
		h.retiring[r.code] = r
		h.m.rooms.Dec()
	}
}

func (h *Hub) unretire(code string) {
	h.mu.Lock()
	delete(h.retiring, code)
	h.mu.Unlock()
}

func (h *Hub) addConn(delta int64) {
	h.m.connections.Add(float64(delta))
	h.conns.Add(delta)
}

// newInstanceID returns a fresh random UUID (version 4) from Config.IDSource.
// Room and match instance ids are not credentials and need no secrecy, only
// uniqueness; an exhausted test source falls back to crypto/rand.
func (h *Hub) newInstanceID() string {
	var b [16]byte
	h.idMu.Lock()
	_, err := io.ReadFull(h.cfg.IDSource, b[:])
	h.idMu.Unlock()
	if err != nil {
		_, _ = rand.Read(b[:])
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// termsState is the outcome of the terms gate for one "join" frame.
type termsState int8

const (
	// termsAccepted: the guest accepted (or the gate is off).
	termsAccepted termsState = iota
	// termsRequired: the guest has not accepted the current terms.
	termsRequired
	// termsUnknown: the gate could not tell (the store failed): fail closed.
	termsUnknown
)

// termsTimeout bounds one terms lookup.
const termsTimeout = 3 * time.Second

// termsState asks the gate about a guest. It runs on the transport's goroutine,
// never inside an actor.
func (h *Hub) termsState(guestID string) termsState {
	gate := h.cfg.Terms
	if gate == nil {
		return termsAccepted
	}
	ctx, cancel := context.WithTimeout(h.baseCtx, termsTimeout)
	defer cancel()
	ok, err := gate.Accepted(ctx, guestID)
	switch {
	case err != nil:
		h.log.Warn("the terms gate could not tell; refusing the join (retryable)", "err", err)
		return termsUnknown
	case !ok:
		return termsRequired
	}
	return termsAccepted
}
