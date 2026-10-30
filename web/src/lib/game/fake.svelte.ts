/**
 * A scriptable `RoomSession` for component tests and the dev playground.
 * Commands are recorded in `sent` (and never touch a network); drive the UI by
 * assigning `view`, calling `emit` / `fail`, or changing `status`.
 *
 *   const session = new FakeSession(lobbyView({ youId: 'p1' }));
 *   render(Lobby, { session });
 *   await user.click(screen.getByRole('button', { name: /empezar/i }));
 *   expect(session.sent).toContainEqual({ cmd: 'startGame', args: [] });
 */
import type { Event as GameEvent, Player, Settings, View } from '$lib/protocol';
import type {
	ClosedReason,
	ConnectionStatus,
	RoomSession,
	SessionError,
	SessionEvent
} from './session';

export interface SentCommand {
	cmd: string;
	args: unknown[];
}

export class FakeSession implements RoomSession {
	code = $state('KT7P');
	status = $state<ConnectionStatus>('connected');
	closedReason = $state<ClosedReason | null>(null);
	joinError = $state<string | null>(null);
	view = $state<View | null>(null);
	events = $state<SessionEvent[]>([]);
	lastError = $state<SessionError | null>(null);
	/** Like `state.canJudge`: the "Ask the AI" button is offered to the host. */
	canJudge = $state(false);
	visibility = $state<'private' | 'public'>('private');
	roomInstanceId = $state<string | null>('room-instance-1');

	/** Every command called, in order. Not reactive. */
	sent: SentCommand[] = [];
	/** When set, `serverNow()` returns this instead of the real clock (deterministic countdowns). */
	frozenNow: number | null = null;
	/** Added to `Date.now()` when `frozenNow` is null. */
	clockOffset = 0;
	/** Dev playground only: `data-testid` of the element the page taps once the screen is up (e.g. an open sheet). */
	devTap: string | null = null;

	#seq = 0;

	constructor(view: View | null = null) {
		this.view = view;
	}

	get players(): readonly Player[] {
		return (this.view?.players ?? []).filter((p) => !p.left);
	}
	get me(): Player | null {
		const v = this.view;
		return v ? (v.players.find((p) => p.id === v.youId && !p.left) ?? null) : null;
	}
	get isHost(): boolean {
		return !!this.view && this.view.hostId === this.view.youId;
	}
	get isChooser(): boolean {
		return !!this.view && !!this.view.chooserId && this.view.chooserId === this.view.youId;
	}
	get isParticipant(): boolean {
		const v = this.view;
		return !!v?.round && v.round.participants.includes(v.youId);
	}

	serverNow(): number {
		return this.frozenNow ?? Date.now() + this.clockOffset;
	}

	/** Replaces the snapshot, like a `state` frame. */
	setView(view: View): void {
		this.view = view;
	}

	/** Announces an engine event, like an `event` frame. */
	emit(event: GameEvent): SessionEvent {
		const e: SessionEvent = { ...event, seq: ++this.#seq, at: this.serverNow() };
		this.events = [...this.events, e].slice(-20);
		return e;
	}

	/** Reports a rejected command, like an `error` frame. */
	fail(code: string, ref?: string): void {
		this.lastError = { code, ref, at: this.serverNow(), seq: ++this.#seq };
	}

	#record(cmd: string, args: unknown[]): void {
		this.sent.push({ cmd, args });
	}

	updateSettings(settings: Settings) {
		this.#record('updateSettings', [settings]);
	}
	kick(playerId: string) {
		this.#record('kick', [playerId]);
	}
	startGame() {
		this.#record('startGame', []);
	}
	stopLetter() {
		this.#record('stopLetter', []);
	}
	setAnswer(category: number, text: string) {
		this.#record('setAnswer', [category, text]);
	}
	basta() {
		this.#record('basta', []);
	}
	askStop() {
		this.#record('askStop', []);
	}
	voteStop(yes: boolean) {
		this.#record('voteStop', [yes]);
	}
	voteReject(category: number, targetPlayer: string, withdraw?: boolean) {
		this.#record(
			'voteReject',
			withdraw === undefined ? [category, targetPlayer] : [category, targetPlayer, withdraw]
		);
	}
	proposeMerge(category: number, playerA: string, playerB: string) {
		this.#record('proposeMerge', [category, playerA, playerB]);
	}
	voteMerge(proposalId: number, yes: boolean) {
		this.#record('voteMerge', [proposalId, yes]);
	}
	reviewNav(category: number) {
		this.#record('reviewNav', [category]);
	}
	askJudge(category: number) {
		this.#record('askJudge', [category]);
	}
	finishReview() {
		this.#record('finishReview', []);
	}
	nextRound() {
		this.#record('nextRound', []);
	}
	endGame() {
		this.#record('endGame', []);
	}
	rematch() {
		this.#record('rematch', []);
	}
	leave() {
		this.#record('leave', []);
		this.closedReason = 'left';
		this.status = 'closed';
	}
	leaveLocally() {
		this.#record('leaveLocally', []);
		this.closedReason = 'left';
		this.status = 'closed';
	}
	destroy() {
		this.#record('destroy', []);
	}
}
