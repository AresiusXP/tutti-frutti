package room

import (
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// maxUnjoinedSessions bounds the connections of a room that have not joined
// (yet): a room has at most game.MaxPlayers players, and a few extra
// connections cover reconnects that overlap their predecessor. Beyond it new
// connections are refused, so that sockets that never join cannot pile up
// (they also expire by Config.JoinTimeout).
const maxUnjoinedSessions = 2 * game.MaxPlayers

// maxPendingFrames bounds the client frames waiting in a room's mailbox. The
// transport already rate-limits every connection (and a room has at most ten
// players), so this is only a safety net: a client that overflows it is
// closed.
const maxPendingFrames = 1024

// task is one unit of work for a room's goroutine. If the room closes before
// the task runs, drop (when set) is called instead, on some other goroutine,
// so that whoever waits for the task never hangs.
type task struct {
	run  func(*room)
	drop func()
}

type pushResult int

const (
	pushed pushResult = iota
	pushClosed
	pushFull
)

// mailbox is the unbounded-by-design (see maxPendingFrames) inbox of a room:
// pushing never blocks, which is what lets Session.Handle promise not to.
type mailbox struct {
	mu     sync.Mutex
	q      []task
	closed bool
	notify chan struct{}
}

func newMailbox() *mailbox { return &mailbox{notify: make(chan struct{}, 1)} }

// push appends t. A positive limit refuses the task when that many are already
// queued.
func (m *mailbox) push(t task, limit int) pushResult {
	m.mu.Lock()
	switch {
	case m.closed:
		m.mu.Unlock()
		return pushClosed
	case limit > 0 && len(m.q) >= limit:
		m.mu.Unlock()
		return pushFull
	}
	m.q = append(m.q, t)
	m.mu.Unlock()
	select {
	case m.notify <- struct{}{}:
	default:
	}
	return pushed
}

// take removes and returns everything queued.
func (m *mailbox) take() []task {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.q
	m.q = nil
	return q
}

// close refuses further tasks and returns the ones still queued.
func (m *mailbox) close() []task {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	q := m.q
	m.q = nil
	return q
}

// session is the room-side handle of one attached Client.
type session struct {
	r  *room
	c  Client
	id string
	// userID is the account the connection is signed in with ("" for a guest).
	userID string

	disconnectOnce sync.Once

	// Owned by the room's goroutine.
	joinBy   time.Time // the deadline of its first successful "join"
	joined   bool      // the first "join" was accepted
	detached bool      // no longer part of the room (left, kicked, replaced, closed...)
	dead     bool      // its Send failed; it is being closed and receives nothing more
}

// refusedSession is what Connect returns for a client the room turned away
// (it was already told and closed): the transport keeps feeding it frames,
// which go nowhere.
type refusedSession struct{}

func (refusedSession) Handle(string, json.RawMessage) {}
func (refusedSession) Disconnect()                    {}

var _ Session = (*session)(nil)

// Handle implements Session.
func (s *session) Handle(t string, d json.RawMessage) {
	d = slices.Clone(d) // the caller may reuse its buffer
	switch s.r.mb.push(task{run: func(r *room) { r.onFrame(s, t, d) }}, maxPendingFrames) {
	case pushFull:
		s.c.Close(protocol.ClosePolicy, "too many pending frames")
	case pushClosed:
		// The room is gone: frames are dropped.
	}
}

// Disconnect implements Session.
func (s *session) Disconnect() {
	s.disconnectOnce.Do(func() {
		s.r.post(func(r *room) { r.onDisconnect(s) }, nil)
	})
}

// room is one game and its actor: a single goroutine (loop) owns every field
// below the "owned by the room's goroutine" line, and everything that wants to
// touch them posts a task to the mailbox.
type room struct {
	h     *Hub
	code  string
	log   *slog.Logger
	clock Clock

	mb    *mailbox
	saver *saver

	// Owned by the room's goroutine.
	g *game.Game
	// users links player ids to the accounts they joined with (guests have no
	// entry); gameID identifies the game in progress ("" in the lobby); pending
	// is the outbox of finished games whose statistics the store has not
	// confirmed yet and inflight the ones the recorder is working on (see
	// statistics below).
	users       map[string]string
	gameID      string
	pending     []store.GameRecord
	inflight    map[string]bool
	legacyFinal bool // restored from a final-phase snapshot of a server without the outbox
	createdAt   time.Time
	lastActive  time.Time
	claimed     bool                  // somebody has joined (selects IdleTTL over UnclaimedTTL)
	sessions    map[*session]struct{} // attached clients, joined or not
	players     map[string]*session   // joined clients by player id
	slow        []*session            // clients whose Send failed, waiting to be reaped
	wake        Timer
	wakeAt      time.Time
	wakeGen     uint64
	nextSnap    time.Time // when the next periodic snapshot is due
	lastSaved   time.Time // when the newest snapshot was handed to the saver
	dirty       bool      // state changed since the last snapshot
	stopping    bool      // the hub is shutting down: no more frames
	closed      bool
}

// post queues run for the room's goroutine. It reports whether the task was
// queued; when the room is closed it calls drop instead.
func (r *room) post(run func(*room), drop func()) bool {
	if r.mb.push(task{run: run, drop: drop}, 0) == pushed {
		return true
	}
	if drop != nil {
		drop()
	}
	return false
}

// loop is the room's goroutine.
func (r *room) loop() {
	for {
		<-r.mb.notify
		batch := r.mb.take()
		for i, t := range batch {
			r.runTask(t)
			if r.closed {
				r.finish(batch[i+1:])
				return
			}
		}
	}
}

// finish refuses further tasks and drops the ones that never ran.
func (r *room) finish(rest []task) {
	rest = append(slices.Clone(rest), r.mb.close()...)
	for _, t := range rest {
		if t.drop != nil {
			t.drop()
		}
	}
}

// runTask runs one task. A panic must not take the whole server down: the
// room is closed (its state may be inconsistent) and the others carry on.
func (r *room) runTask(t task) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("room panicked; closing it", "panic", p, "stack", string(debug.Stack()))
			r.closeAfterPanic()
		}
	}()
	t.run(r)
	if len(r.slow) > 0 {
		r.reapSlow()
	}
}

func (r *room) closeAfterPanic() {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("closing a panicked room failed", "panic", p)
		}
	}()
	r.closeRoom(protocol.CloseRoomClosed, "internal error", true)
}

// ---- requests from other goroutines ----

type infoResult struct {
	info Info
	ok   bool
}

// info returns the room's public summary; it is ordered after every task
// posted before it.
func (r *room) info() (Info, bool) {
	ch := make(chan infoResult, 1)
	r.post(func(r *room) { ch <- infoResult{r.summary(), true} }, func() { ch <- infoResult{} })
	res := <-ch
	return res.info, res.ok
}

// connect attaches c and returns its session once the room has accepted it.
func (r *room) connect(c Client) (Session, error) {
	s := &session{r: r, c: c, id: c.PlayerID()}
	if u, ok := c.(UserLinked); ok {
		s.userID = u.UserID()
	}
	ack := make(chan error, 1)
	r.post(func(r *room) { ack <- r.onConnect(s) }, func() { ack <- ErrRoomClosed })
	switch err := <-ack; {
	case errors.Is(err, errRefused):
		return refusedSession{}, nil
	case err != nil:
		return nil, err
	}
	return s, nil
}

// errRefused is onConnect's answer for a client it turned away.
var errRefused = errors.New("room: connection refused")

// ---- tasks (run on the room's goroutine) ----

func (r *room) summary() Info {
	seated := r.seated()
	return Info{
		Code:       r.code,
		Phase:      r.g.Phase,
		Players:    seated,
		MaxPlayers: game.MaxPlayers,
		Lang:       r.g.Settings.Lang,
		Joinable:   !r.stopping && seated < game.MaxPlayers && r.g.Phase != game.PhaseFinal,
	}
}

func (r *room) seated() int {
	n := 0
	for _, p := range r.g.Players {
		if !p.Left {
			n++
		}
	}
	return n
}

func (r *room) onInit() {
	now := r.clock.Now()
	r.lastActive = now
	r.resumeStats(now)
	r.persist(now)
	r.rearm()
}

func (r *room) onConnect(s *session) error {
	if r.stopping || r.closed {
		return ErrShuttingDown
	}
	if r.unjoined() >= maxUnjoinedSessions {
		r.refuse(s.c)
		return errRefused
	}
	s.joinBy = r.clock.Now().Add(r.h.cfg.JoinTimeout)
	r.sessions[s] = struct{}{}
	r.h.addConn(1)
	r.rearm()
	return nil
}

// unjoined is the number of attached clients that have not joined.
func (r *room) unjoined() int { return len(r.sessions) - len(r.players) }

// refuse tells a client the room is busy and closes it. The transport hands
// the client its first frame right after Connect returns, so the error says
// it is the answer to a "join".
func (r *room) refuse(c Client) {
	r.log.Debug("refusing a connection: too many connections waiting to join", "player", c.PlayerID())
	r.h.m.command(game.CmdJoin, protocol.ErrServerBusy)
	if frame := r.frame(protocol.TypeError, protocol.ErrorBody{Code: protocol.ErrServerBusy, Ref: game.CmdJoin}); frame != nil {
		c.Send(frame)
	}
	c.Close(protocol.ClosePolicy, "too many connections")
}

// expireUnjoined closes the clients whose join deadline passed.
func (r *room) expireUnjoined(now time.Time) {
	for s := range r.sessions {
		if !s.joined && !now.Before(s.joinBy) {
			r.detach(s)
			s.c.Close(protocol.ClosePolicy, "join timeout")
		}
	}
}

func (r *room) onDisconnect(s *session) {
	if s.detached {
		return
	}
	r.detach(s)
	if r.stopping {
		return
	}
	now := r.clock.Now()
	if !s.joined {
		r.rearm()
		return
	}
	prev := r.g.Phase
	events, err := r.g.SetConnected(now, s.id, false)
	if err != nil && !errors.Is(err, game.ErrUnknownPlayer) {
		r.log.Error("marking a seat disconnected failed", "player", s.id, "err", err)
	}
	r.commit(now, prev, events, false)
}

func (r *room) onFrame(s *session, t string, d json.RawMessage) {
	if s.detached || r.stopping || r.closed {
		return
	}
	now := r.clock.Now()
	switch {
	case t == game.CmdJoin:
		r.onJoin(now, s, d)
	case !s.joined:
		r.reject(s, t, protocol.ErrNotJoined)
	default:
		r.onCommand(now, s, t, d)
	}
}

func (r *room) onJoin(now time.Time, s *session, d json.RawMessage) {
	if s.joined {
		r.reject(s, game.CmdJoin, protocol.ErrAlreadyJoined)
		return
	}
	var req protocol.JoinRequest
	if err := json.Unmarshal(d, &req); err != nil || !strings.EqualFold(strings.TrimSpace(req.Code), r.code) {
		r.reject(s, game.CmdJoin, protocol.ErrBadMessage)
		return
	}

	prev := r.g.Phase
	events, err := r.g.Apply(now, s.id, game.Join{Name: req.Name, Avatar: req.Avatar})
	if err != nil {
		r.rejectEngine(now, s, game.CmdJoin, prev, events, err)
		return
	}
	more, err := r.g.SetConnected(now, s.id, true)
	events = append(events, more...)
	if err != nil { // the seat was just created or reclaimed
		r.log.Error("marking a seat connected failed", "player", s.id, "err", err)
		r.rejectEngine(now, s, game.CmdJoin, prev, events, err)
		return
	}

	if old := r.players[s.id]; old != nil && old != s {
		r.sendTo(old, r.frame(protocol.TypeEvent, game.Event{Type: protocol.EventReplaced}))
		r.detach(old)
		old.c.Close(protocol.CloseReplaced, "replaced by another connection")
	}
	s.joined = true
	r.players[s.id] = s
	r.linkUser(s)
	r.claimed = true
	r.lastActive = now
	r.h.m.command(game.CmdJoin, resultOK)
	r.commit(now, prev, events, true)
}

// lobbyChanges are the commands that change who is in the room or how it is
// set up; the room is saved after each of them (phase changes are saved too).
var lobbyChanges = map[string]bool{
	game.CmdLeave: true, game.CmdKick: true, game.CmdUpdateSettings: true,
	game.CmdStartGame: true, game.CmdRematch: true,
}

func (r *room) onCommand(now time.Time, s *session, t string, d json.RawMessage) {
	cmd, err := game.ParseCommand(t, d)
	if err != nil {
		r.reject(s, t, protocol.ErrBadMessage)
		return
	}
	prev := r.g.Phase
	events, err := r.g.Apply(now, s.id, cmd)
	if err != nil {
		r.rejectEngine(now, s, t, prev, events, err)
		return
	}
	r.h.m.command(t, resultOK)

	_, leaving := cmd.(game.Leave)
	if leaving {
		// The leaver is out of the room before the others are told, and is
		// closed after they were.
		r.detach(s)
	}
	r.commit(now, prev, events, lobbyChanges[t])
	if leaving {
		s.c.Close(protocol.CloseLeft, "left")
	}
}

// rejectEngine answers an engine error to the sender. The engine may still
// have applied due transitions before failing, and everybody must hear of
// those.
func (r *room) rejectEngine(now time.Time, s *session, t string, prev game.Phase, events []game.Event, err error) {
	r.reject(s, t, game.ErrorCode(err))
	if len(events) > 0 {
		r.commit(now, prev, events, false)
	}
}

// reject sends an error frame to s only.
func (r *room) reject(s *session, t, code string) {
	r.h.m.command(t, code)
	r.sendTo(s, r.frame(protocol.TypeError, protocol.ErrorBody{Code: code, Ref: t}))
}

// onWake runs when the room's single timer fires: it closes the connections
// that did not join in time, closes an idle room, applies the engine's due
// transitions and takes the snapshots that are due (periodic and heartbeat).
func (r *room) onWake(gen uint64) {
	if r.closed || r.stopping || gen != r.wakeGen {
		return
	}
	r.wake, r.wakeAt = nil, time.Time{}
	now := r.clock.Now()
	r.expireUnjoined(now)
	if len(r.players) == 0 && !now.Before(r.idleDeadline()) {
		r.log.Info("closing idle room", "claimed", r.claimed)
		r.closeRoom(protocol.CloseRoomClosed, "room closed", true)
		return
	}
	prev := r.g.Phase
	if events := r.g.Tick(now); len(events) > 0 {
		r.commit(now, prev, events, false)
		return
	}
	if r.saveDue(now) {
		r.persist(now)
	}
	r.rearm()
}

// beginShutdown announces the restart and takes the final snapshot. From now
// on the room ignores client frames.
func (r *room) beginShutdown() (seq uint64, ok bool) {
	if r.closed || r.stopping {
		return 0, false
	}
	r.stopping = true
	r.stopWake()
	if frame := r.frame(protocol.TypeEvent, game.Event{Type: protocol.EventServerRestarting}); frame != nil {
		for s := range r.sessions {
			r.sendTo(s, frame)
		}
	}
	now := r.clock.Now()
	data, err := r.encode(now)
	if err != nil {
		r.log.Error("encoding the final snapshot failed", "err", err)
		r.h.m.snapshotErrors.Inc()
		return 0, false
	}
	return r.saver.submit(data), true
}

// closeRoom closes every attached client with code and stops the room. With
// deleteSnapshot its saved state is removed (the room is gone for good);
// without it the snapshot stays for the next process to restore.
func (r *room) closeRoom(code int, reason string, deleteSnapshot bool) {
	if r.closed {
		return
	}
	r.closed = true
	r.stopWake()
	for _, s := range slices.Collect(maps.Keys(r.sessions)) {
		r.detach(s)
		s.c.Close(code, reason)
	}
	if deleteSnapshot {
		r.handOffPending()
	}
	r.h.retire(r)
	r.saver.finish(deleteSnapshot)
}

// ---- state changes ----

// commit is what every state change goes through: tell everybody, close the
// room if nobody is left in it, save what needs saving and re-arm the timer.
// prev is the phase before the engine call; lobbyChange asks for a snapshot.
func (r *room) commit(now time.Time, prev game.Phase, events []game.Event, lobbyChange bool) {
	r.countEvents(events)
	r.trackStats(now, events)
	r.broadcast(now, events)
	if r.seated() == 0 {
		r.closeRoom(protocol.CloseRoomClosed, "everyone left", true)
		return
	}
	r.dirty = true
	if lobbyChange || r.g.Phase != prev {
		r.persist(now)
	}
	r.rearm()
}

func (r *room) countEvents(events []game.Event) {
	for _, ev := range events {
		switch ev.Type {
		case game.EventRoundStarted:
			r.h.m.rounds.Inc()
		case game.EventBasta:
			r.h.m.basta.Inc()
		}
	}
}

// broadcast sends one event frame per engine event to every joined client,
// then each joined client its own state. A kicked player's client is closed
// right after it received its "kicked" event.
func (r *room) broadcast(now time.Time, events []game.Event) {
	for _, ev := range events {
		if frame := r.frame(protocol.TypeEvent, ev); frame != nil {
			for _, s := range r.joined() {
				r.sendTo(s, frame)
			}
		}
		if ev.Type == game.EventKicked {
			if s := r.players[ev.PlayerID]; s != nil {
				r.detach(s)
				s.c.Close(protocol.CloseKicked, "kicked")
			}
		}
	}
	for _, s := range r.joined() {
		r.sendTo(s, r.frame(protocol.TypeState, protocol.State{
			Code:       r.code,
			ServerTime: now.UnixMilli(),
			View:       r.g.View(s.id),
		}))
	}
}

// joined returns the joined sessions.
func (r *room) joined() []*session {
	return slices.Collect(maps.Values(r.players))
}

// sendTo delivers a frame without ever blocking: a client whose queue is full
// is closed (it reconnects and gets a fresh snapshot) and reaped at the end of
// the task.
func (r *room) sendTo(s *session, frame []byte) {
	if frame == nil || s.detached || s.dead {
		return
	}
	if !s.c.Send(frame) {
		s.dead = true
		r.slow = append(r.slow, s)
		s.c.Close(protocol.ClosePolicy, "slow client")
	}
}

// reapSlow treats the clients that could not keep up as disconnected right
// away (the transport's own Disconnect then finds them gone).
func (r *room) reapSlow() {
	for len(r.slow) > 0 {
		s := r.slow[0]
		r.slow = r.slow[1:]
		r.onDisconnect(s)
	}
}

// detach removes s from the room. It is idempotent.
func (r *room) detach(s *session) {
	if s.detached {
		return
	}
	s.detached = true
	delete(r.sessions, s)
	if r.players[s.id] == s {
		delete(r.players, s.id)
	}
	if s.joined {
		// The room was active while this client was in it: the idle time
		// counts from now (the player may have been there for hours).
		r.lastActive = r.clock.Now()
	}
	r.h.addConn(-1)
}

func (r *room) frame(t string, v any) []byte {
	d, err := json.Marshal(v)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(protocol.Envelope{T: t, V: protocol.Version, D: d}); err == nil {
			return b
		}
	}
	r.log.Error("encoding a frame failed", "type", t, "err", err)
	return nil
}

// ---- statistics ----
//
// A finished game goes through three stages, all owned by the room's goroutine
// except the delivery itself:
//
//  1. At game_started the room mints the game's id, which every snapshot of the
//     game carries; a game replayed after a crash (restored from a snapshot taken
//     before game_over) therefore ends under the same id and counts once.
//  2. At game_over the room builds the store.GameRecord once, appends it to its
//     outbox (pending, part of every snapshot) and offers it to the recorder. The
//     record is never rebuilt from live state afterwards, so nothing that happens
//     later (a reconnect, a rematch) can change who gets credit.
//  3. When the recorder reports that the store has the record, the room removes it
//     from the outbox and saves. A record that the recorder gave up on stays in the
//     outbox: it is offered again at the room's next snapshot and by the next
//     process at its start.

// linkUser remembers which account the player that just joined is signed in
// with. Without a Recorder there is nothing to link for.
//
// The link follows the latest join only until the player takes part in a round
// of the game (is a participant of the current round or has a scored round):
// from then on it is frozen until the rematch, absent links included. Whoever
// holds a player's guest cookie (the bearer credential of the seat) can
// reconnect as somebody else, and this is what stops them from moving the credit
// of rounds that were already played to another account; and a player who signs
// out, or whose session expired, does not lose it. The price: signing in after the
// game started earns no credit for that game.
func (r *room) linkUser(s *session) {
	if r.h.recorder == nil || r.tookPart(s.id) {
		return
	}
	if s.userID == "" {
		delete(r.users, s.id)
		return
	}
	if r.users == nil {
		r.users = map[string]string{}
	}
	r.users[s.id] = s.userID
}

// tookPart reports whether the player is a participant of the round in progress
// or appears in the scored history of the game.
func (r *room) tookPart(id string) bool {
	if r.g.Round != nil && slices.Contains(r.g.Round.Participants, id) {
		return true
	}
	return slices.ContainsFunc(r.g.History, func(rr game.RoundResult) bool {
		_, ok := rr.Players[id]
		return ok
	})
}

// trackStats reacts to the start and the end of a game and to the rematch, and
// drops the links of seats that no longer exist. It runs before the room is
// saved, so the snapshot of a phase change carries the game id and the outbox.
func (r *room) trackStats(now time.Time, events []game.Event) {
	if r.h.recorder == nil {
		return
	}
	for _, ev := range events {
		switch ev.Type {
		case game.EventGameStarted:
			r.gameID = store.NewID()
		case game.EventGameOver:
			r.gameOver(now)
		case game.EventRematch:
			r.gameID = ""
		}
	}
	// Seats removed from the lobby or by the rematch take their link with them;
	// a seat flagged Left keeps it (it still gets credit for the rounds it
	// played).
	for id := range r.users {
		if !slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.ID == id }) {
			delete(r.users, id)
		}
	}
}

// gameOver freezes the result of the game that just ended into the outbox and
// offers it for delivery. A game that adds nothing (see buildRecord) leaves
// nothing behind.
func (r *room) gameOver(now time.Time) {
	if r.gameID == "" { // a game that did not announce its start
		r.gameID = store.NewID()
	}
	rec, ok := buildRecord(r.g, r.users, r.gameID, now)
	if !ok {
		return
	}
	r.addPending(rec)
	if !r.offer(rec) {
		r.log.Warn("game statistics not queued; they stay in the outbox and are offered again later", "game", rec.ID)
	}
}

// addPending appends rec to the outbox. Beyond maxPendingRecords the oldest
// record is given up (and counted as dropped); the recorder may still deliver it
// if it already has it.
func (r *room) addPending(rec store.GameRecord) {
	if slices.ContainsFunc(r.pending, func(p store.GameRecord) bool { return p.ID == rec.ID }) {
		return
	}
	r.pending = append(r.pending, rec)
	if over := len(r.pending) - maxPendingRecords; over > 0 {
		for _, old := range r.pending[:over] {
			r.h.m.statsRecorded.WithLabelValues(statsDropped).Inc()
			r.log.Warn("outbox full: giving up the statistics of the oldest game", "game", old.ID)
		}
		r.pending = slices.Clone(r.pending[over:])
	}
}

// offer hands a record to the recorder unless it already has it. It reports
// whether the recorder has it (false: closing or full; the record stays in the
// outbox).
func (r *room) offer(rec store.GameRecord) bool {
	if r.inflight[rec.ID] {
		return true
	}
	id := rec.ID
	job := statsJob{rec: rec, done: func(o statsOutcome) {
		r.post(func(r *room) { r.onStatsDone(id, o) }, nil)
	}}
	if !r.h.recorder.enqueue(job) {
		return false
	}
	if r.inflight == nil {
		r.inflight = map[string]bool{}
	}
	r.inflight[id] = true
	return true
}

// offerPending offers every record of the outbox the recorder does not have.
func (r *room) offerPending() {
	if r.h.recorder == nil {
		return
	}
	for _, rec := range slices.Clone(r.pending) {
		r.offer(rec)
	}
}

// onStatsDone is the recorder's report (see statsOutcome). A record the store
// has, or refused for good, leaves the outbox; a stalled one stays.
func (r *room) onStatsDone(id string, o statsOutcome) {
	delete(r.inflight, id)
	if o == outcomeStalled {
		return
	}
	i := slices.IndexFunc(r.pending, func(p store.GameRecord) bool { return p.ID == id })
	if i < 0 {
		return
	}
	r.pending = slices.Delete(r.pending, i, i+1)
	if r.stopping || r.closed {
		return
	}
	now := r.clock.Now()
	r.persist(now)
	r.rearm()
}

// handOffPending is called when the room is closed for good: its snapshot is
// about to be deleted, so the records the recorder does not have yet are offered
// one last time (the recorder keeps them for as long as the process lives).
func (r *room) handOffPending() {
	if r.h.recorder == nil {
		return
	}
	for _, rec := range r.pending {
		if !r.offer(rec) {
			r.h.m.statsRecorded.WithLabelValues(statsDropped).Inc()
			r.log.Warn("room closed with game statistics nobody can take: given up", "game", rec.ID)
		}
	}
}

// restoreStats takes the account state of a restored snapshot.
func (r *room) restoreStats(p Persisted) {
	r.users, r.gameID, r.pending = p.Users, p.GameID, p.Pending
	r.legacyFinal = p.Outbox == 0 && p.GameID != "" && r.g.Phase == game.PhaseFinal && len(p.Pending) == 0
	for range p.droppedPending {
		r.h.m.statsRecorded.WithLabelValues(statsDropped).Inc()
	}
	if p.droppedPending > 0 {
		r.log.Warn("discarded unreadable game statistics from the snapshot", "records", p.droppedPending)
	}
}

// resumeStats runs when a restored room starts: a started game without an id
// (a snapshot from before games had one) gets one, a final-phase snapshot of a
// server without the outbox rebuilds its record (it may have died before it was
// recorded), and everything in the outbox is offered again (recording is
// idempotent).
func (r *room) resumeStats(now time.Time) {
	if r.h.recorder == nil {
		return
	}
	if r.g.Phase != game.PhaseLobby && r.gameID == "" {
		r.gameID = store.NewID()
	}
	if r.legacyFinal {
		r.legacyFinal = false
		if rec, ok := buildRecord(r.g, r.users, r.gameID, now); ok {
			r.addPending(rec)
		}
	}
	r.offerPending()
}

// ---- persistence and timers ----

// activeAt is the time somebody was last in the room: now while any client
// has joined.
func (r *room) activeAt(now time.Time) time.Time {
	if len(r.players) > 0 {
		return now
	}
	return r.lastActive
}

// encode is the room's snapshot: its game, its account links, the id of the
// game in progress and the outbox of games whose statistics are not confirmed.
func (r *room) encode(now time.Time) ([]byte, error) {
	return encodeRoomState(Persisted{
		Code:       r.code,
		CreatedAt:  r.createdAt,
		LastActive: r.activeAt(now),
		Users:      r.users,
		GameID:     r.gameID,
		Pending:    r.pending,
		Outbox:     outboxVersion,
	}, r.g)
}

// persist marshals the room inside the actor and hands the bytes to the
// saver, which writes them in the background.
func (r *room) persist(now time.Time) {
	// Whatever happens, the next snapshot is not due before its interval: a
	// failing encoder must not make the timer fire in a loop.
	r.lastSaved = now
	r.nextSnap = now.Add(r.h.cfg.SnapshotEvery)
	// A record the recorder gave up on is offered again with every snapshot.
	r.offerPending()
	data, err := r.encode(now)
	if err != nil {
		r.log.Error("encoding the snapshot failed", "err", err)
		r.h.m.snapshotErrors.Inc()
		return
	}
	r.saver.submit(data)
	r.dirty = false
}

// needsPeriodicSave: answers change all the time while a round is being
// played, so those phases are saved every SnapshotEvery; the review is only
// saved when something changed.
func (r *room) needsPeriodicSave() bool {
	switch r.g.Phase {
	case game.PhasePlaying, game.PhaseGrace:
		return true
	case game.PhaseReview:
		return r.dirty
	}
	return false
}

func (r *room) periodicDue(now time.Time) bool {
	return r.needsPeriodicSave() && !now.Before(r.nextSnap)
}

// heartbeatDue: a room that is alive saves at least every Hub.heartbeat, so
// the janitor (which deletes the snapshots older than SnapshotMaxAge) never
// takes the snapshot of a room that is still there, however quiet it is.
func (r *room) heartbeatDue(now time.Time) bool {
	return !now.Before(r.lastSaved.Add(r.h.heartbeat))
}

func (r *room) saveDue(now time.Time) bool {
	return r.periodicDue(now) || r.heartbeatDue(now)
}

func (r *room) idleDeadline() time.Time {
	ttl := r.h.cfg.IdleTTL
	if !r.claimed {
		ttl = r.h.cfg.UnclaimedTTL
	}
	return r.lastActive.Add(ttl)
}

// nextWake is the earliest of the things the room is waiting for: the
// engine's next deadline, the next periodic snapshot, the heartbeat snapshot,
// the join deadline of every client that has not joined and, while nobody has
// joined, the end of its idle time.
func (r *room) nextWake() time.Time {
	at := r.lastSaved.Add(r.h.heartbeat)
	consider := func(t time.Time) {
		if t.Before(at) {
			at = t
		}
	}
	if d, ok := r.g.NextDeadline(); ok {
		consider(d)
	}
	if r.needsPeriodicSave() {
		consider(r.nextSnap)
	}
	if len(r.players) == 0 {
		consider(r.idleDeadline())
	}
	for s := range r.sessions {
		if !s.joined {
			consider(s.joinBy)
		}
	}
	return at
}

// rearm keeps exactly one timer armed at nextWake, replacing the previous one
// only when the target moved.
func (r *room) rearm() {
	if r.closed || r.stopping {
		r.stopWake()
		return
	}
	at := r.nextWake()
	if r.wake != nil && r.wakeAt.Equal(at) {
		return
	}
	r.stopWake()
	gen := r.wakeGen
	// AfterFunc counts from the clock's current time, so measure the delay from
	// a fresh reading rather than from the (older) time of the task.
	r.wake = r.clock.AfterFunc(max(at.Sub(r.clock.Now()), 0), func() {
		r.post(func(r *room) { r.onWake(gen) }, nil)
	})
	r.wakeAt = at
}

// stopWake cancels the armed timer. The generation counter makes a fire that
// already happened (and is queued) harmless.
func (r *room) stopWake() {
	r.wakeGen++
	if r.wake != nil {
		r.wake.Stop()
		r.wake = nil
		r.wakeAt = time.Time{}
	}
}
