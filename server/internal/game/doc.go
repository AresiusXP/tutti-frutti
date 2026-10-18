// Package game is the pure Tutti Frutti engine: rules, scoring and the game
// state machine. It performs no I/O, starts no goroutines, never reads the
// clock (every method that needs time takes `now`) and uses no randomness.
//
// # Usage
//
// A room owns one *Game and drives it from a single goroutine:
//
//	events, err := g.Apply(now, playerID, cmd)         // one player intent
//	events, err = g.SetConnected(now, playerID, true)  // socket opened/closed
//	events = g.Tick(now)                               // when NextDeadline() fires
//	view := g.View(playerID)                           // filtered snapshot per client
//
// Commands come from clients (ParseCommand turns a WebSocket message into
// one). Connection state does not: the room layer owns the sockets and is the
// only authority on who is connected, through the trusted Game.SetConnected.
// "set_connected" is rejected by ParseCommand with ErrUnknownCommand, and a
// wire-level Join never touches Connected or ConnectedAt of an existing seat,
// so a client cannot change its own flag and skew the live vote thresholds or
// the host election.
//
// What the room layer must do on a socket event:
//
//   - A socket opens for a player: call Apply(Join) (it creates the seat for a
//     new player, or reclaims a seat flagged Left, and is otherwise a harmless
//     no-op that just validates the seat), and then, if it succeeded, call
//     SetConnected(now, id, true). A brand-new seat is created connected, so
//     the second call is a no-op for it. A reclaimed seat keeps its previous
//     Connected value (leaving disconnected it) until SetConnected says
//     otherwise.
//   - A socket closes: call SetConnected(now, id, false). The seat is kept.
//
// The whole Game is JSON-serialisable in every phase (snapshots); View is the
// filtered snapshot sent to clients.
//
// # Rules and decisions
//
// DESIGN.md sections 3 to 5 are the source of truth. Where they leave room,
// the engine follows these readings:
//
//   - Deadlines are exclusive. A transition happens when now >= deadline, so
//     an answer arriving exactly at GraceEndsAt+LateUpdateAllowance is
//     rejected and one 1 ns earlier is accepted. Apply runs Tick(now) before
//     handling a command, so commands always see the up-to-date phase.
//   - Ticks catch up. Every transition is dated at its own deadline rather than
//     at the time the tick is processed: the chooser auto-stop reveals the
//     letter at LetterStartedAt+ChooserTimeout, so the time limit starts there
//     even if the tick is late, and Tick then applies any later deadline that
//     has also passed (a very late tick can go letter -> playing -> grace ->
//     review in one call, emitting letter_revealed, time_up, round_locked).
//     A manual stop_letter is dated at the time it is received.
//   - Time-limit expiry enters the grace phase with no caller (CallerID is
//     empty), with GraceEndsAt set to the limit itself: inputs are locked
//     from that instant and the phase only lasts for LateUpdateAllowance.
//     No basta event is emitted (time_up is).
//   - The review has no timer; the host drives it. Reject votes and merge
//     proposals are evaluated live with the connected participants at
//     evaluation time (a vote from a disconnected player stops counting).
//   - A reject vote needs yes*2 > eligible (connected participants other than
//     the author). A merge proposal passes when yes*2 > connected
//     participants; the proposer's vote is a yes.
//   - Answers are compared after Normalize: invisible format characters
//     (Unicode category Cf: zero width space, joiners, soft hyphen, BOM, bidi
//     marks, ...) removed, trimmed, whitespace collapsed, lower-cased,
//     NFD-decomposed with every combining mark dropped (so NFC and NFD
//     spellings agree and any diacritic goes: "Ắng" == "ang"), plus a few
//     non-decomposable letters folded (ø, đ, ð, ł, ...). The Dutch ligature
//     "ĳ" / "Ĳ" (U+0133 / U+0132) becomes "ij" in every language, so "Ĳsland",
//     "ĳsland" and "IJsland" are one answer and start with "I" (never with "Y").
//     In Spanish lobbies "ñ" (NFC or NFD) is kept as its own letter; in English
//     and Dutch it folds to "n". The duplicate check of custom categories
//     normalizes with the lobby language and compares against the built-in
//     labels of every language.
//     StartsWithLetter uses the same function for answer and letter. The text
//     a player types (nickname, custom category label, answer) is stored with
//     the format characters removed too, so a value made only of them is empty
//     (ErrInvalidName, an empty custom label, an unfilled answer).
//   - Voting to reject twice is a no-op; VoteReject.Withdraw removes the vote.
//     Votes on empty answers are refused (ErrEmptyAnswer).
//   - Participants of a round are the seated players with JoinedRound <= round
//     number at the moment the round starts. Players who leave mid-round stay
//     participants (their answers still count) but can no longer vote.
//   - In the lobby, leaving or being kicked removes the seat; once the game
//     started the seat is flagged Left so scores and history stay intact.
//     Kicked ids are banned for the life of the game (ErrKicked). A player who
//     left voluntarily can join again: in the lobby as a new seat, in a started
//     game by reclaiming their old seat (score and join order included, with
//     Connected unchanged: see the room layer notes above).
//     A brand-new player cannot join in the final phase (ErrWrongPhase): they
//     would be swept into the rematch without having played. They can join
//     again once the host starts the rematch and the game is back in the lobby.
//     (Reclaiming a seat, or a join from a seated player, is still allowed.)
//   - The chooser rotation follows JoinedSeq: the host in round 1, then the
//     next participant with a higher JoinedSeq than the previous chooser,
//     wrapping around and skipping players who left. Disconnected players are
//     not skipped (their 10 s timer auto-stops the alphabet).
//   - Host transfer on leave/kick: the connected player with the earliest
//     ConnectedAt (ties by JoinedSeq); if nobody is connected, the earliest
//     joiner. A merely disconnected host stays host (SetConnected never moves
//     the role).
//   - Winners are the seated players with the highest total (ties give several
//     winners); players who left cannot win.
//   - end_game and next_round are only valid from round_summary. Rematch is
//     only valid from final and drops players who left.
//   - View hides the letter-phase clock: no timestamp that would let a client
//     derive the letter is ever sent.
package game
