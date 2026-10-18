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
package room
