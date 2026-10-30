// # Actor model
//
// Every room is one goroutine (room.loop) that owns its *game.Game and all of
// its bookkeeping, so none of it needs a lock. Everything else talks to it by
// posting a task to its mailbox: client frames (Session.Handle), connects and
// disconnects, timer fires, info requests and shutdown steps. Posting never
// blocks, which is why Session.Handle can promise not to, and tasks of one room
// run strictly in the order they were posted. The Hub only keeps the registry
// (code -> room) behind a mutex; it never holds that mutex while waiting for a
// room, and rooms only take it for a moment (retire), so there is no lock
// order to get wrong.
//
// Database I/O never runs in an actor. The one lookup a join needs, the terms
// gate (Config.Terms), runs on the transport's goroutine inside Session.Handle
// and travels to the actor with the frame; report evidence is a mailbox task
// (Hub.ReportEvidence) whose answer comes back over a channel.
//
// The room never waits for a client: Client.Send is non-blocking and a client
// whose queue is full is closed with protocol.ClosePolicy and treated as
// disconnected (it reconnects and receives a fresh state).
//
// After every change (a command, a tick, a connect or a disconnect) the room
// sends one event frame per engine event to every joined client, then each of
// them its own filtered state frame.
//
// # Connections that have not joined
//
// Connect attaches a client without seating it: it is seated by its first
// successful "join". Until then it is an unjoined session, and the room
// treats it as a liability rather than as a player:
//
//   - It has a deadline, Config.JoinTimeout (10 s) after Connect, by which a
//     join must have succeeded; otherwise it is closed with
//     protocol.ClosePolicy ("join timeout"). A rejected join (lobby_full,
//     invalid_name, ...) leaves the session attached but does not move the
//     deadline, and neither does anything the transport answers on its own, such
//     as a ping.
//   - A room holds at most 2*game.MaxPlayers unjoined sessions. Beyond that
//     Connect still returns a Session (the transport is written for it) but
//     the room has already sent the client error server_busy and closed it with
//     protocol.ClosePolicy, and the session ignores everything. The refused
//     client is not counted in Stats.
//   - It does not count as activity: it does not claim a room (a room is
//     "claimed", and so subject to IdleTTL instead of UnclaimedTTL, only after a
//     successful join) and it does not keep an otherwise empty room from being
//     closed as idle.
//
// # Timers
//
// A room has a single timer, armed at the earliest of: the engine's next
// deadline (Game.NextDeadline), the next periodic snapshot (while a round is
// played), the heartbeat snapshot, the join deadline of its unjoined sessions
// and, while no client has joined, the end of its idle time (IdleTTL, or
// UnclaimedTTL for a room nobody ever joined). It is replaced after every task
// only if that instant moved. When it fires the room closes the sessions whose
// join deadline passed, closes itself if it is idle, otherwise applies
// Game.Tick (late ticks catch up by engine design) and takes the snapshots that
// are due. No per-session goroutines or timers exist.
//
// # Persistence
//
// The room marshals itself (Persisted) inside its goroutine, which is the only
// place the state can be read consistently, and hands the bytes to its saver:
// a goroutine that writes to the store with retry and exponential backoff.
// Only the newest snapshot matters, so a snapshot submitted while an older one
// is still being written or retried replaces it ("latest wins"); a slow or dead
// store therefore never slows the room down or accumulates a backlog. A room is
// saved on every phase change, after every lobby change (join, leave, kick,
// settings, start, rematch), every SnapshotEvery while a round is played and,
// whatever its phase, at least every SnapshotMaxAge/4 (the heartbeat).
//
// The heartbeat is what makes the janitor safe: Hub.Start's janitor deletes
// every snapshot older than SnapshotMaxAge, and a room that is alive but quiet
// (an unchanged lobby, the final podium) would otherwise be saved once and then
// never again. With the heartbeat the snapshot of a live room is never older
// than a quarter of SnapshotMaxAge, so only the snapshots of rooms that are gone
// (a crashed process's leftovers) are old enough to be collected.
//
// Hub.Start restores the saved rooms with every player disconnected and the idle
// clock restarted; snapshots that cannot be decoded are deleted. It is meant to
// be called once, before the hub serves anything: it never replaces a room that
// is already live (or whose code a closing room still holds); such a snapshot is
// skipped with a warning and left alone.
//
// A room that is closed for good (idle, or everybody left) deletes its
// snapshot. The deletion is retried, with capped exponential backoff and without
// a limit, until the store accepts it, and the room's code stays reserved until
// then: a new room must not reuse a code whose old row is still stored (a late
// delete would remove the new room's snapshot, and a restart would bring the old
// room back under the new one's code). Hub.Shutdown stops retrying (the process
// is exiting and the stale snapshot is the next process's to discard); it keeps
// the snapshots of live rooms, after writing a final one per room and waiting
// for it, so that the next process picks the games up where they were.
//
// # Accounts and statistics
//
// A Client that implements UserLinked carries the account its connection was
// signed in with. A successful "join" links the player to that user, the room
// keeps player -> user in its snapshot (Persisted.Users, absent in older
// snapshots) and drops a link with its seat (a seat that merely left a started
// game keeps it: it still gets credit for the rounds it played, until the
// rematch). The link follows the latest join only until the player takes part in
// the game (a participant of the round in progress or a player with a scored
// round); from then on it is frozen until the rematch, absent links included, so
// that reconnecting with another account's session cannot move the credit of
// rounds already played (the guest cookie is the bearer credential of a seat).
//
// A finished game goes through three stages, all inside the room's goroutine
// except the delivery:
//
//   - At game_started the room mints the game's id. Every snapshot of the game
//     carries it (Persisted.GameID), so a game replayed after a crash, from a
//     snapshot taken before game_over, ends under the same id and the store
//     counts it once. A restored started game without an id gets one.
//   - At game_over the room builds the store.GameRecord once (buildRecord: the
//     players linked to a user who took part in a scored round of a game with at
//     least two participants), appends it to its outbox (room.pending, saved as
//     Persisted.Pending, at most maxPendingRecords) and offers it to the
//     recorder. The record is never rebuilt afterwards, so nothing that happens
//     later can change who gets credit.
//   - The recorder is one goroutine with a bounded queue that retries a failing
//     store with exponential backoff on the hub's clock. It reports back to the
//     room through a posted task (statsJob.done): when the store has the record
//     (or refused it for good) the room removes it from the outbox and saves; a
//     record the recorder gave up on stays in the outbox and is offered again with
//     the room's next snapshot, by the next process at its start (onInit), and
//     once more when the room is closed for good. The room never waits for the
//     recorder and never fails because of it.
//
// Hub.Shutdown lets the recorder give every queued game one last attempt once the
// rooms stopped, within the shutdown budget; the final snapshots carry the outbox,
// so whatever still fails is recorded by the next process. A final-phase snapshot
// written before the outbox existed (a game id but no Persisted.Outbox marker)
// rebuilds its record once at restore. Without Config.Recorder none of this
// exists.
//
// # Deleting an account
//
// A seat is the guest's; the account only adds links on top of it (the
// connection's, Persisted.Users, Persisted.Accounts, the authors of custom
// labels and the outbox). Hub.ForgetAccount, called by DELETE /api/me after the
// store deleted the account, remembers the id and posts a task to every room that
// removes all of those links, saves the room and tells the players their state
// changed (canJudge); the seat, its scores and its answers stay. A connection
// that was identified before the deletion and attaches after it is made a guest
// by onConnect. Hub.Start does the same for the accounts deleted while the
// process was down, by looking up the accounts a snapshot names before the room
// is rebuilt. The order and the argument are in docs/DESIGN.md, section 9b.
package room
