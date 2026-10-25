/**
 * The contract between the room screens and the network layer.
 *
 * Screens (`$lib/screens/*.svelte`) receive a `RoomSession` and never touch
 * WebSockets, cookies or the protocol envelope. The real implementation lives
 * in `$lib/game/client.svelte.ts` (WebSocket, reconnect, join); tests and the
 * dev playground use `FakeSession` from `$lib/game/fake.svelte.ts`.
 *
 * Every property below is REACTIVE (Svelte 5 runes): read it inside
 * `$derived`/templates and the UI follows. Commands never throw: a rejected
 * command (not host, wrong phase, ...) arrives later through `lastError`.
 */
import type { Event as GameEvent, Player, Settings, View } from '$lib/protocol';

export type ConnectionStatus =
	/** First connection in progress (before the first state arrived). */
	| 'connecting'
	/** Socket open and a state snapshot received. */
	| 'connected'
	/** Lost the socket; retrying with backoff. `view` keeps the last snapshot. */
	| 'reconnecting'
	/** Finished for good: see `closedReason`. */
	| 'closed';

/** Why a session is closed for good (no more reconnects). */
export type ClosedReason =
	/** The room does not exist (any more): HTTP 404 `room_not_found` or close 4004. */
	| 'room_gone'
	/** The host kicked us (close 4002 / `kicked` event). */
	| 'kicked'
	/** We left on purpose (close 4005 / `leave()`). */
	| 'left'
	/** Another tab/device of the same player took the seat (close 4001). */
	| 'replaced'
	/** Protocol version mismatch (close 4003): the app must be reloaded. */
	| 'unsupported_version'
	/** The join was refused: see `joinError`. */
	| 'join_rejected';

export interface SessionError {
	/** Stable snake_case code: a `Code*` from the engine or an `Err*` from the protocol, or `not_connected`. */
	code: string;
	/** The command (`t`) that caused it, when known. */
	ref?: string;
	/** `Date.now()` when it arrived. */
	at: number;
	/** Local, monotonically increasing id: lets screens react to *new* errors only. */
	seq: number;
}

/** An engine event with a local id and arrival time (events are announcements; state is the truth). */
export interface SessionEvent extends GameEvent {
	seq: number;
	at: number;
}

export interface RoomSession {
	/** The 4-character room code. */
	readonly code: string;
	readonly status: ConnectionStatus;
	readonly closedReason: ClosedReason | null;
	/** Error code of a refused join when `closedReason === 'join_rejected'` (e.g. `lobby_full`, `invalid_name`). */
	readonly joinError: string | null;

	/** Latest snapshot for this player; `null` until the first state arrives. */
	readonly view: View | null;
	/** Seated players that did not leave (`view.players` minus `left`). */
	readonly players: readonly Player[];
	/** This player's seat, if seated. */
	readonly me: Player | null;
	readonly isHost: boolean;
	/** True when this player chooses the letter in the current round. */
	readonly isChooser: boolean;
	/** True when this player takes part in the current round (mid-game joiners wait for the next one). */
	readonly isParticipant: boolean;
	/** True when the server has an AI judge and this player is signed in (latest `state`; false when absent). */
	readonly canJudge: boolean;

	/** The last 20 events, oldest first. */
	readonly events: readonly SessionEvent[];
	/** The most recent rejected command / protocol error (kept until replaced). */
	readonly lastError: SessionError | null;

	/** Server clock estimate in ms since the epoch: `Date.now() + offset`. Use it for every deadline. */
	serverNow(): number;

	// --- commands (host-only ones are marked) -------------------------------
	/** Host, lobby only. Sends the complete settings object. */
	updateSettings(settings: Settings): void;
	/** Host. */
	kick(playerId: string): void;
	/** Host, lobby, >= 2 players. */
	startGame(): void;
	/** Chooser, letter phase. */
	stopLetter(): void;
	/**
	 * Sets this player's answer for a category (index into `view.settings.categories`).
	 * Works while playing/grace (the server accepts updates for 500 ms after the
	 * lock). When offline the latest text per category is kept and flushed after
	 * the next successful join.
	 */
	setAnswer(category: number, text: string): void;
	/** Calls Basta; allowed only when every category holds a valid answer. */
	basta(): void;
	/**
	 * Asks every other connected participant to stop although some answers are
	 * missing (needs `settings.stopVote` and no open vote). Pending answers are
	 * flushed first; the vote itself arrives as `view.round.stopVote`.
	 */
	askStop(): void;
	/** Answers the open vote to stop (never the asker's own). Any "no" rejects it at once. */
	voteStop(yes: boolean): void;
	/** Votes to reject `targetPlayer`'s answer in `category` (review). `withdraw` removes the vote. */
	voteReject(category: number, targetPlayer: string, withdraw?: boolean): void;
	/** Proposes that two answers of a category are the same (review). */
	proposeMerge(category: number, playerA: string, playerB: string): void;
	voteMerge(proposalId: number, yes: boolean): void;
	/** Host, review: show another category to everyone. */
	reviewNav(category: number): void;
	/** Host (signed in, `canJudge`), review: asks the AI to judge the answers of a category. Advisory: scores never change. */
	askJudge(category: number): void;
	/** Host, review: finish the review and score the round. */
	finishReview(): void;
	/** Host, round summary. */
	nextRound(): void;
	/** Host, round summary: end the game now. */
	endGame(): void;
	/** Host, final: back to the lobby with the same settings and players (scores reset). */
	rematch(): void;
	/**
	 * Leaves the room for good and closes the session (`closedReason === 'left'`).
	 * While offline the session keeps reconnecting for a few seconds so the server
	 * really hears the leave (it frees the seat and passes the host role on); if the
	 * network stays down it closes locally anyway (`LEAVE_GRACE_MS`).
	 */
	leave(): void;
	/** Stops reconnecting and releases the socket (call from `onDestroy`). Does not leave the room. */
	destroy(): void;
}
