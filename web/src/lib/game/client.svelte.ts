/**
 * The real `RoomSession`: one WebSocket to the Go server with reconnects.
 *
 * Connect flow (every attempt): `GET /api/session` (once per session, sets the
 * guest cookie) -> `GET /api/rooms/{code}` preflight (404 closes for good,
 * 429/5xx/network retry) -> open `/ws` -> send `join` as the FIRST frame ->
 * the first `state` means connected.
 *
 * It never gives up on 502/503/504/network errors: the server is a single
 * replica and every deploy leaves a gap of a few seconds. Only the close
 * codes 4001-4005, an unknown room and a refused join are final.
 * See `$lib/game/session.ts` for the contract and docs/DESIGN.md (9a, 9b).
 */
/* eslint-disable svelte/prefer-svelte-reactivity -- the Maps below are private bookkeeping the UI never reads */
import { ApiError, createApi, type Api } from '$lib/api';
import type { Avatar } from '$lib/avatar';
import { profile as profileStore } from '$lib/profile.svelte';
import {
	CloseKicked,
	CloseLeft,
	CloseReplaced,
	CloseRoomClosed,
	CloseServiceRestart,
	CloseUnsupportedVersion,
	CmdAskJudge,
	CmdAskStop,
	CmdBasta,
	CmdEndGame,
	CmdFinishReview,
	CmdJoin,
	CmdKick,
	CmdLeave,
	CmdNextRound,
	CmdProposeMerge,
	CmdRematch,
	CmdReviewNav,
	CmdSetAnswer,
	CmdStartGame,
	CmdStopLetter,
	CmdUpdateSettings,
	CmdVoteMerge,
	CmdVoteReject,
	CmdVoteStop,
	CodeInternal,
	CodeKicked,
	ErrRateLimited,
	ErrRoomClosed,
	ErrRoomNotFound,
	ErrServerBusy,
	ErrUnsupportedVersion,
	EventKicked,
	EventReplaced,
	EventServerRestarting,
	PingInterval,
	TypeError,
	TypeEvent,
	TypePing,
	TypePong,
	TypeState,
	type AskJudge,
	type ErrorBody,
	type Event as GameEvent,
	type JoinRequest,
	type Kick,
	type Player,
	type Pong,
	type ProposeMerge,
	type ReviewNav,
	type SetAnswer,
	type Settings,
	type State,
	type UpdateSettings,
	type View,
	type VoteMerge,
	type VoteReject,
	type VoteStop
} from '$lib/protocol';
import { Backoff } from './backoff';
import type {
	ClosedReason,
	ConnectionStatus,
	RoomSession,
	SessionError,
	SessionEvent
} from './session';
import { WS_OPEN, browserSocket, decodeFrame, encodeFrame, wsUrl } from './transport';
import type { SocketFactory, SocketLike } from './transport';

/** Nothing at all received for this long on an open socket: it is dead, reconnect. */
export const IDLE_TIMEOUT_MS = 45_000;
/** A socket that has not opened after this long is abandoned. */
export const CONNECT_TIMEOUT_MS = 10_000;
/** After waking up (tab visible, network back) a ping must be answered within this. */
export const PROBE_TIMEOUT_MS = 5_000;
/** At most one `set_answer` frame per category in this window (the latest text wins). */
export const ANSWER_GAP_MS = 100;
/** A connection that died sooner than this after joining does not reset the backoff. */
export const STABLE_MS = 3_000;
/** How long `leave()` keeps trying to reach the server while offline before leaving locally anyway. */
export const LEAVE_GRACE_MS = 4_000;
const MAX_RETRY_AFTER_MS = 10_000;
const EVENT_RING = 20;
const CLOCK_SAMPLES = 8;
const PING_QUEUE = 3;

export interface EventSource {
	addEventListener(type: string, listener: () => void): void;
	removeEventListener(type: string, listener: () => void): void;
}

/** Everything the session needs from the outside world (all injectable for tests). */
export interface SessionDeps {
	createSocket: SocketFactory;
	fetch: (input: string, init?: RequestInit) => Promise<Response>;
	/** Wall clock in ms (`Date.now`). */
	now: () => number;
	setTimeout: (fn: () => void, ms: number) => unknown;
	clearTimeout: (handle: unknown) => void;
	/** In [0, 1): jitter of the backoff. */
	random: () => number;
	/** Name and avatar sent with every `join` (read on each attempt). */
	profile: () => { name: string; avatar: Avatar };
	/** Where the page lives: decides `ws:` vs `wss:` and the host. */
	location: { protocol: string; host: string };
	/** Emits `online`. */
	win: EventSource | null;
	/** Emits `visibilitychange`; `visibilityState` is read when it fires. */
	doc: (EventSource & { visibilityState: string }) | null;
}

function defaultDeps(): SessionDeps {
	return {
		createSocket: browserSocket,
		fetch: (input, init) => globalThis.fetch(input, init),
		now: () => Date.now(),
		setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
		clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
		random: () => Math.random(),
		profile: () => ({ name: profileStore.nickname, avatar: profileStore.avatar }),
		location: globalThis.location,
		win: typeof window === 'undefined' ? null : window,
		doc: typeof document === 'undefined' ? null : document
	};
}

type TimerName = 'retry' | 'heartbeat' | 'watchdog' | 'probe' | 'leave';

interface PendingAnswer {
	text: string;
	/** Round it was typed in (null when unknown), so a stale answer never lands in the next round. */
	round: number | null;
}

interface ClockSample {
	rtt: number;
	offset: number;
}

export class WsRoomSession implements RoomSession {
	readonly code: string;

	#status = $state<ConnectionStatus>('connecting');
	#closedReason = $state<ClosedReason | null>(null);
	#joinError = $state<string | null>(null);
	#view = $state.raw<View | null>(null);
	#events = $state.raw<SessionEvent[]>([]);
	#lastError = $state.raw<SessionError | null>(null);
	#canJudge = $state(false);

	readonly #d: SessionDeps;
	readonly #api: Api;
	readonly #backoff: Backoff;

	/** No more connecting: closed for good or destroyed. */
	#stopped = false;
	/** leave() was called while offline: send it as soon as the join succeeds. */
	#leavePending = false;
	#seq = 0;
	#sessionReady = false;

	#socket: SocketLike | null = null;
	/** The current socket got its first `state` (the join was accepted). */
	#joined = false;
	#joinedAt: number | null = null;
	#joinSentAt = 0;
	#abort: AbortController | null = null;
	#attemptId = 0;

	#timers = new Map<TimerName, unknown>();
	#idleLimit = CONNECT_TIMEOUT_MS;
	#lastRx = 0;
	#rxCount = 0;
	#pingTimes: number[] = [];

	#offset = 0;
	#samples: ClockSample[] = [];

	#pending = new Map<number, PendingAnswer>();
	#answerTimers = new Map<number, unknown>();
	#answerSentAt = new Map<number, number>();
	#sentAnswers = new Map<number, string>();
	#answerRound: number | null = null;

	constructor(code: string, deps: Partial<SessionDeps> = {}) {
		this.code = code.toUpperCase();
		this.#d = { ...defaultDeps(), ...deps };
		this.#api = createApi(this.#d.fetch);
		this.#backoff = new Backoff(this.#d.random);
		this.#d.win?.addEventListener('online', this.#wake);
		this.#d.doc?.addEventListener('visibilitychange', this.#onVisibility);
		void this.#connect();
	}

	// --- RoomSession: state ---------------------------------------------------

	get status(): ConnectionStatus {
		return this.#status;
	}
	get closedReason(): ClosedReason | null {
		return this.#closedReason;
	}
	get joinError(): string | null {
		return this.#joinError;
	}
	get view(): View | null {
		return this.#view;
	}
	get events(): readonly SessionEvent[] {
		return this.#events;
	}
	get lastError(): SessionError | null {
		return this.#lastError;
	}
	get players(): readonly Player[] {
		return (this.#view?.players ?? []).filter((p) => !p.left);
	}
	get me(): Player | null {
		const v = this.#view;
		return v ? (v.players.find((p) => p.id === v.youId && !p.left) ?? null) : null;
	}
	get isHost(): boolean {
		const v = this.#view;
		return !!v && v.hostId === v.youId;
	}
	get isChooser(): boolean {
		const v = this.#view;
		return !!v && !!v.chooserId && v.chooserId === v.youId;
	}
	get isParticipant(): boolean {
		const v = this.#view;
		return !!v?.round && v.round.participants.includes(v.youId);
	}

	get canJudge(): boolean {
		return this.#canJudge;
	}

	serverNow(): number {
		return this.#d.now() + this.#offset;
	}

	// --- RoomSession: commands ------------------------------------------------

	updateSettings(settings: Settings) {
		const d: UpdateSettings = { settings };
		this.#command(CmdUpdateSettings, d);
	}
	kick(playerId: string) {
		const d: Kick = { playerId };
		this.#command(CmdKick, d);
	}
	startGame() {
		this.#command(CmdStartGame);
	}
	stopLetter() {
		this.#command(CmdStopLetter);
	}
	setAnswer(category: number, text: string) {
		if (this.#stopped) return;
		this.#pending.set(category, { text, round: this.#view?.round?.number ?? null });
		if (this.#isConnected()) this.#scheduleAnswer(category);
	}
	basta() {
		if (!this.#ready(CmdBasta)) return;
		this.#flushAnswersNow();
		this.#sendFrame(CmdBasta);
	}
	askStop() {
		if (!this.#ready(CmdAskStop)) return;
		this.#flushAnswersNow();
		this.#sendFrame(CmdAskStop);
	}
	voteStop(yes: boolean) {
		const d: VoteStop = { yes };
		this.#command(CmdVoteStop, d);
	}
	voteReject(category: number, targetPlayer: string, withdraw?: boolean) {
		const d: VoteReject =
			withdraw === undefined ? { category, targetPlayer } : { category, targetPlayer, withdraw };
		this.#command(CmdVoteReject, d);
	}
	proposeMerge(category: number, playerA: string, playerB: string) {
		const d: ProposeMerge = { category, playerA, playerB };
		this.#command(CmdProposeMerge, d);
	}
	voteMerge(proposalId: number, yes: boolean) {
		const d: VoteMerge = { proposalId, yes };
		this.#command(CmdVoteMerge, d);
	}
	reviewNav(category: number) {
		const d: ReviewNav = { category };
		this.#command(CmdReviewNav, d);
	}
	askJudge(category: number) {
		const d: AskJudge = { category };
		this.#command(CmdAskJudge, d);
	}
	finishReview() {
		this.#command(CmdFinishReview);
	}
	nextRound() {
		this.#command(CmdNextRound);
	}
	endGame() {
		this.#command(CmdEndGame);
	}
	rematch() {
		this.#command(CmdRematch);
	}
	leave() {
		if (this.#stopped) return;
		if (this.#isConnected()) {
			this.#sendFrame(CmdLeave);
			return this.#finish('left');
		}
		// Never seated (no state yet): nothing to give up on the server.
		if (!this.#view) return this.#finish('left');
		// Offline with a seat: the server only frees the seat (and passes the host
		// role on) if it hears `leave`. Keep reconnecting for a few seconds and send
		// it as soon as the join succeeds; if the network stays down, leave locally
		// anyway so the player is never stuck on the screen.
		if (this.#leavePending) return;
		this.#leavePending = true;
		this.#startTimer('leave', LEAVE_GRACE_MS, () => this.#finish('left'));
	}
	destroy() {
		this.#shutdown();
	}

	// --- connecting -------------------------------------------------------------

	async #connect(): Promise<void> {
		if (this.#stopped) return;
		this.#stopTimer('retry');
		this.#dropSocket();
		const attempt = ++this.#attemptId;
		const abort = new AbortController();
		this.#abort = abort;
		const live = () => attempt === this.#attemptId && !this.#stopped;
		const opts = { signal: abort.signal };

		if (!this.#sessionReady) {
			try {
				await this.#api.ensureSession(opts);
			} catch (e) {
				if (!live()) return;
				// A broken /api/session that is not a transient failure must not block the room.
				if (e instanceof ApiError && !e.retryable) this.#sessionReady = true;
				else return this.#preflightFailed(e);
			}
			if (!live()) return;
			this.#sessionReady = true;
		}

		try {
			await this.#api.getRoom(this.code, opts);
		} catch (e) {
			if (!live()) return;
			return this.#preflightFailed(e);
		}
		if (!live()) return;
		this.#openSocket();
	}

	#preflightFailed(e: unknown): void {
		if (e instanceof ApiError && e.status === 404) return this.#finish('room_gone');
		const err = e instanceof ApiError ? e : null;
		const busy = err?.code === 'server_busy';
		const minDelay = Math.min((err?.retryAfter ?? 0) * 1000, MAX_RETRY_AFTER_MS);
		this.#scheduleRetry({ busy, minDelay });
	}

	#openSocket(): void {
		let ws: SocketLike;
		try {
			ws = this.#d.createSocket(wsUrl(this.#d.location));
		} catch {
			return this.#scheduleRetry();
		}
		this.#socket = ws;
		this.#joined = false;
		this.#pingTimes = [];
		ws.onopen = () => {
			if (this.#socket === ws) this.#onOpen();
		};
		ws.onmessage = (ev) => {
			if (this.#socket === ws) this.#onMessage(ev.data);
		};
		ws.onclose = (ev) => {
			if (this.#socket === ws) this.#onClose(ev.code);
		};
		ws.onerror = () => {
			// a `close` event always follows
		};
		this.#lastRx = this.#d.now();
		this.#idleLimit = CONNECT_TIMEOUT_MS;
		this.#armWatchdog(CONNECT_TIMEOUT_MS);
	}

	#onOpen(): void {
		const now = this.#d.now();
		this.#lastRx = now;
		this.#idleLimit = IDLE_TIMEOUT_MS;
		this.#armWatchdog(IDLE_TIMEOUT_MS);
		this.#joinSentAt = now;
		const { name, avatar } = this.#d.profile();
		const join: JoinRequest = { code: this.code, name, avatar };
		this.#sendFrame(CmdJoin, join);
		this.#armHeartbeat();
	}

	#onClose(code: number): void {
		switch (code) {
			case CloseReplaced:
				return this.#finish('replaced');
			case CloseKicked:
				return this.#finish('kicked');
			case CloseUnsupportedVersion:
				return this.#finish('unsupported_version');
			case CloseRoomClosed:
				return this.#finish('room_gone');
			case CloseLeft:
				return this.#finish('left');
			default:
				// The server contract: never reconnect after a 40xx code. One we do not
				// know is a newer server's way of saying the room is over.
				if (code >= 4000 && code <= 4999) return this.#finish('room_gone');
				// 1006/1001/1008/1009/1000 and anything else: the player keeps the seat, reconnect.
				return this.#scheduleRetry({ busy: code === CloseServiceRestart });
		}
	}

	/** Tears the socket down and retries later with backoff (never when stopped). */
	#scheduleRetry(opts: { busy?: boolean; minDelay?: number } = {}): void {
		if (this.#stopped) return;
		this.#settleConnection();
		this.#status = this.#view ? 'reconnecting' : 'connecting';
		const delay = Math.max(this.#backoff.next({ busy: opts.busy }), opts.minDelay ?? 0);
		this.#startTimer('retry', delay, () => void this.#connect());
	}

	/** Releases the current socket; a connection that held for a while resets the backoff. */
	#settleConnection(): void {
		if (this.#joinedAt !== null) {
			if (this.#d.now() - this.#joinedAt >= STABLE_MS) this.#backoff.reset();
			this.#joinedAt = null;
		}
		this.#abort?.abort();
		this.#dropSocket();
		this.#clearAnswerTimers();
	}

	#dropSocket(): void {
		const ws = this.#socket;
		this.#socket = null;
		this.#joined = false;
		this.#stopTimer('heartbeat');
		this.#stopTimer('watchdog');
		this.#stopTimer('probe');
		if (!ws) return;
		ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
		try {
			ws.close(1000);
		} catch {
			// already closed
		}
	}

	/** Wake-ups (network back, tab visible): connect NOW instead of waiting for the backoff timer. */
	#wake = (): void => {
		if (this.#stopped) return;
		if (this.#status === 'connected' && this.#socket?.readyState === WS_OPEN) {
			// A suspended tab may have lost its socket without being told: check right away.
			if (this.#d.now() - this.#lastRx >= IDLE_TIMEOUT_MS) return this.#reconnectNow();
			return this.#probe();
		}
		this.#reconnectNow();
	};

	#onVisibility = (): void => {
		if (this.#d.doc?.visibilityState === 'visible') this.#wake();
	};

	#reconnectNow(): void {
		this.#backoff.reset();
		this.#stopTimer('retry');
		this.#settleConnection();
		this.#status = this.#view ? 'reconnecting' : 'connecting';
		void this.#connect();
	}

	#probe(): void {
		if (!this.#sendPing()) return;
		const seen = this.#rxCount;
		this.#startTimer('probe', PROBE_TIMEOUT_MS, () => {
			if (this.#rxCount === seen) this.#scheduleRetry();
		});
	}

	// --- heartbeat / dead connection ------------------------------------------------

	#armHeartbeat(): void {
		this.#startTimer('heartbeat', PingInterval, () => {
			if (this.#sendPing()) this.#armHeartbeat();
		});
	}

	#sendPing(): boolean {
		if (this.#socket?.readyState !== WS_OPEN) return false;
		this.#pingTimes.push(this.#d.now());
		if (this.#pingTimes.length > PING_QUEUE) this.#pingTimes.shift();
		return this.#sendFrame(TypePing);
	}

	/** One timer, re-armed for the remainder: no churn on every incoming frame. */
	#armWatchdog(ms: number): void {
		this.#startTimer('watchdog', ms, () => {
			const idle = this.#d.now() - this.#lastRx;
			if (idle >= this.#idleLimit) this.#scheduleRetry();
			else this.#armWatchdog(this.#idleLimit - idle);
		});
	}

	// --- incoming frames ------------------------------------------------------------

	#onMessage(data: unknown): void {
		this.#lastRx = this.#d.now();
		this.#rxCount++;
		const env = decodeFrame(data);
		if (!env) return;
		switch (env.t) {
			case TypeState:
				return this.#onState(env.d as State | undefined);
			case TypeEvent:
				return this.#onEvent(env.d as GameEvent | undefined);
			case TypeError:
				return this.#onError(env.d as ErrorBody | undefined);
			case TypePong:
				return this.#onPong(env.d as Pong | undefined);
		}
	}

	#onState(state: State | undefined): void {
		if (!state || typeof state.view !== 'object' || state.view === null) return;
		const view = state.view;
		const first = !this.#joined;
		this.#view = view;
		this.#canJudge = state.canJudge === true;
		const round = view.round?.number ?? null;
		if (first || round !== this.#answerRound) this.#sentAnswers.clear();
		this.#answerRound = round;
		if (!first) return;

		const now = this.#d.now();
		this.#joined = true;
		this.#joinedAt = now;
		this.#status = 'connected';
		this.#samples = [];
		this.#sampleClock(state.serverTime, this.#joinSentAt, now);
		if (this.#leavePending) {
			this.#sendFrame(CmdLeave);
			return this.#finish('left');
		}
		this.#flushPendingAfterJoin();
	}

	#onEvent(e: GameEvent | undefined): void {
		if (!e || typeof e.type !== 'string') return;
		const event: SessionEvent = { ...e, seq: ++this.#seq, at: this.#d.now() };
		this.#events = [...this.#events, event].slice(-EVENT_RING);
		if (e.type === EventReplaced) {
			this.#finish('replaced');
		} else if (e.type === EventKicked && !!e.playerId && e.playerId === this.#view?.youId) {
			this.#finish('kicked');
		} else if (e.type === EventServerRestarting) {
			// the 1012 close follows; the replacement pod needs a few seconds
			this.#backoff.markBusy();
		}
	}

	#onError(body: ErrorBody | undefined): void {
		if (!body || typeof body.code !== 'string') return;
		if (!this.#joined) return this.#joinRejected(body.code, body.ref);
		this.#setError(body.code, body.ref);
		switch (body.code) {
			case ErrUnsupportedVersion:
				return this.#finish('unsupported_version');
			case ErrRoomNotFound:
			case ErrRoomClosed:
				return this.#finish('room_gone');
		}
	}

	/** An error before the first `state` of a socket: the join was refused. */
	#joinRejected(code: string, ref?: string): void {
		// Transient: the server is draining or hiccuping; retry quietly (no error toast per attempt).
		if (code === ErrServerBusy) return this.#scheduleRetry({ busy: true });
		if (code === ErrRateLimited || code === CodeInternal) return this.#scheduleRetry();
		this.#setError(code, ref);
		switch (code) {
			case ErrUnsupportedVersion:
				return this.#finish('unsupported_version');
			case ErrRoomNotFound:
			case ErrRoomClosed:
				return this.#finish('room_gone');
			case CodeKicked:
				return this.#finish('kicked');
			default:
				return this.#finish('join_rejected', code);
		}
	}

	#onPong(pong: Pong | undefined): void {
		const sentAt = this.#pingTimes.shift();
		if (sentAt === undefined || !pong) return;
		this.#sampleClock(pong.serverTime, sentAt, this.#d.now());
	}

	/** Estimates `server - local` from a request sent at `sentAt` and answered at `receivedAt`; keeps the lowest-latency sample. */
	#sampleClock(serverTime: number, sentAt: number, receivedAt: number): void {
		if (!Number.isFinite(serverTime)) return;
		this.#samples.push({
			rtt: Math.max(0, receivedAt - sentAt),
			offset: serverTime - (sentAt + receivedAt) / 2
		});
		if (this.#samples.length > CLOCK_SAMPLES) this.#samples.shift();
		this.#offset = this.#samples.reduce((a, b) => (b.rtt < a.rtt ? b : a)).offset;
	}

	// --- finishing --------------------------------------------------------------------

	#finish(reason: ClosedReason, joinError: string | null = null): void {
		if (this.#stopped) return;
		this.#closedReason = reason;
		this.#joinError = joinError;
		this.#status = 'closed';
		this.#shutdown();
	}

	/** Stops everything: socket, timers, listeners. Idempotent. */
	#shutdown(): void {
		this.#stopped = true;
		this.#attemptId++;
		this.#abort?.abort();
		this.#abort = null;
		this.#stopTimer('retry');
		this.#stopTimer('leave');
		this.#dropSocket();
		this.#clearAnswerTimers();
		this.#pending.clear();
		this.#d.win?.removeEventListener('online', this.#wake);
		this.#d.doc?.removeEventListener('visibilitychange', this.#onVisibility);
	}

	// --- sending ----------------------------------------------------------------------

	#isConnected(): boolean {
		return this.#status === 'connected' && this.#joined && this.#socket?.readyState === WS_OPEN;
	}

	#sendFrame(t: string, d?: unknown): boolean {
		const ws = this.#socket;
		if (!ws || ws.readyState !== WS_OPEN) return false;
		try {
			ws.send(encodeFrame(t, d));
			return true;
		} catch {
			return false;
		}
	}

	/** True when a command can go out now; otherwise reports `not_connected` (nothing at all after `destroy()`). */
	#ready(t: string): boolean {
		if (this.#isConnected()) return true;
		if (this.#status === 'closed' || !this.#stopped) this.#setError('not_connected', t);
		return false;
	}

	#command(t: string, d?: object): void {
		if (this.#ready(t)) this.#sendFrame(t, d);
	}

	#setError(code: string, ref?: string): void {
		this.#lastError = { code, ref, at: this.#d.now(), seq: ++this.#seq };
	}

	// --- answers ------------------------------------------------------------------------

	/** What the server holds for this category: what we sent on this socket, else its own view. */
	#known(category: number): string {
		const sent = this.#sentAnswers.get(category);
		if (sent !== undefined) return sent;
		const v = this.#view;
		return v?.round?.answers?.[v.youId]?.[category] ?? '';
	}

	/** Leading edge sends at once; a burst within the gap is coalesced into one trailing frame with the latest text. */
	#scheduleAnswer(category: number): void {
		if (this.#answerTimers.has(category)) return;
		const wait = (this.#answerSentAt.get(category) ?? -Infinity) + ANSWER_GAP_MS - this.#d.now();
		if (wait <= 0) return this.#sendAnswer(category);
		this.#answerTimers.set(
			category,
			this.#d.setTimeout(() => {
				this.#answerTimers.delete(category);
				this.#sendAnswer(category);
			}, wait)
		);
	}

	#sendAnswer(category: number): void {
		const answer = this.#pending.get(category);
		if (!answer) return;
		if (!this.#isConnected()) return; // stays pending for the next join
		this.#pending.delete(category);
		if (answer.text === this.#known(category)) return;
		const d: SetAnswer = { category, text: answer.text };
		if (this.#sendFrame(CmdSetAnswer, d)) {
			this.#sentAnswers.set(category, answer.text);
			this.#answerSentAt.set(category, this.#d.now());
		} else {
			this.#pending.set(category, answer);
		}
	}

	/** Before `basta` the server must hold the latest text of every category. */
	#flushAnswersNow(): void {
		this.#clearAnswerTimers();
		for (const category of [...this.#pending.keys()]) this.#sendAnswer(category);
	}

	/** Answers typed while offline: sent right after the first state of the new socket, unless stale. */
	#flushPendingAfterJoin(): void {
		const v = this.#view;
		const playing = v?.phase === 'playing' || v?.phase === 'grace';
		for (const [category, answer] of [...this.#pending]) {
			const stale =
				!playing ||
				!this.isParticipant ||
				(answer.round !== null && answer.round !== v?.round?.number);
			if (stale) this.#pending.delete(category);
		}
		this.#flushAnswersNow();
	}

	#clearAnswerTimers(): void {
		for (const handle of this.#answerTimers.values()) this.#d.clearTimeout(handle);
		this.#answerTimers.clear();
	}

	// --- timers -------------------------------------------------------------------------

	#startTimer(name: TimerName, ms: number, fn: () => void): void {
		this.#stopTimer(name);
		this.#timers.set(
			name,
			this.#d.setTimeout(() => {
				this.#timers.delete(name);
				fn();
			}, ms)
		);
	}

	#stopTimer(name: TimerName): void {
		const handle = this.#timers.get(name);
		if (handle === undefined) return;
		this.#d.clearTimeout(handle);
		this.#timers.delete(name);
	}
}
