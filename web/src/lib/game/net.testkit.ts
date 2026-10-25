/**
 * Test doubles for the network layer: a scriptable WebSocket, a routable
 * `fetch` and a harness that wires them into `WsRoomSession`. Not a test file.
 */
import { vi } from 'vitest';
import { Version, type Envelope, type View } from '$lib/protocol';
import { makeView, player } from './fixtures';
import { WsRoomSession, type SessionDeps } from './client.svelte';
import type { SocketLike } from './transport';

export class FakeSocket implements SocketLike {
	readyState = 0;
	onopen: ((ev: unknown) => void) | null = null;
	onmessage: ((ev: { data: unknown }) => void) | null = null;
	onclose: ((ev: { code: number }) => void) | null = null;
	onerror: ((ev: unknown) => void) | null = null;
	raw: string[] = [];
	closedWith: { code?: number; reason?: string } | null = null;

	constructor(readonly url: string) {}

	send(data: string): void {
		if (this.readyState !== 1) throw new Error('socket is not open');
		this.raw.push(data);
	}
	close(code?: number, reason?: string): void {
		this.closedWith = { code, reason };
		this.readyState = 3;
	}

	/** Frames the client sent, parsed. */
	get frames(): Envelope[] {
		return this.raw.map((r) => JSON.parse(r) as Envelope);
	}
	/** Types of the frames sent (join, ping, ...). */
	get types(): string[] {
		return this.frames.map((f) => f.t);
	}

	open(): void {
		this.readyState = 1;
		this.onopen?.({});
	}
	receive(t: string, d?: unknown): void {
		this.onmessage?.({
			data: JSON.stringify(d === undefined ? { t, v: Version } : { t, v: Version, d })
		});
	}
	receiveRaw(data: unknown): void {
		this.onmessage?.({ data });
	}
	/** `canJudge` left out sends a frame without the field (an older server). */
	state(view: View, serverTime = Date.now(), code = 'KT7P', canJudge?: boolean): void {
		this.receive('state', {
			code,
			serverTime,
			view,
			...(canJudge === undefined ? {} : { canJudge })
		});
	}
	serverClose(code: number): void {
		this.readyState = 3;
		this.onclose?.({ code });
	}
}

export class FakeTarget {
	listeners = new Map<string, Set<() => void>>();
	visibilityState = 'visible';
	addEventListener(type: string, fn: () => void): void {
		if (!this.listeners.has(type)) this.listeners.set(type, new Set());
		this.listeners.get(type)!.add(fn);
	}
	removeEventListener(type: string, fn: () => void): void {
		this.listeners.get(type)?.delete(fn);
	}
	emit(type: string): void {
		for (const fn of [...(this.listeners.get(type) ?? [])]) fn();
	}
	count(): number {
		return [...this.listeners.values()].reduce((n, set) => n + set.size, 0);
	}
}

export type Reply = Response | Error;

export function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json', ...headers }
	});
}

export interface Call {
	url: string;
	init?: RequestInit;
}

export interface Harness {
	session: WsRoomSession;
	sockets: FakeSocket[];
	calls: Call[];
	win: FakeTarget;
	doc: FakeTarget;
	/** Replies for `/api/session` and `/api/rooms/KT7P`, in order; the last one repeats. */
	replies: { session: Reply[]; room: Reply[] };
	/** The newest socket. */
	sock(): FakeSocket;
	settle(): Promise<void>;
	advance(ms: number): Promise<void>;
	/** Opens the newest socket and answers its join with a state. */
	join(view?: View, serverTime?: number): Promise<FakeSocket>;
	/** Number of `/api/rooms/` preflights so far. */
	roomCalls(): number;
}

export const ME = player('p1', 'Ana');

export function lobbyView(over: Partial<View> = {}): View {
	return makeView({ youId: 'p1', hostId: 'p1', players: [ME, player('p2', 'Beto')], ...over });
}

export interface HarnessOptions {
	code?: string;
	random?: () => number;
	session?: Reply[];
	room?: Reply[];
	profile?: SessionDeps['profile'];
	location?: SessionDeps['location'];
	createSocket?: SessionDeps['createSocket'];
}

/** Builds a `WsRoomSession` with fake sockets, fetch, timers (use `vi.useFakeTimers()`) and listeners. */
export function harness(opts: HarnessOptions = {}): Harness {
	const sockets: FakeSocket[] = [];
	const calls: Call[] = [];
	const win = new FakeTarget();
	const doc = new FakeTarget();
	const replies = {
		session: opts.session ?? [json({ guest: true })],
		room: opts.room ?? [
			json({ code: 'KT7P', phase: 'lobby', players: 1, maxPlayers: 10, lang: 'es', joinable: true })
		]
	};
	const counters = { session: 0, room: 0 };
	const answer = (kind: 'session' | 'room'): Response => {
		const list = replies[kind];
		const reply = list[Math.min(counters[kind]++, list.length - 1)];
		if (reply instanceof Error) throw reply;
		return reply.clone();
	};

	const session = new WsRoomSession(opts.code ?? 'kt7p', {
		createSocket:
			opts.createSocket ??
			((url) => {
				const s = new FakeSocket(url);
				sockets.push(s);
				return s;
			}),
		fetch: (input, init) => {
			calls.push({ url: input, init });
			try {
				return Promise.resolve(answer(input.startsWith('/api/session') ? 'session' : 'room'));
			} catch (e) {
				return Promise.reject(e as Error);
			}
		},
		random: opts.random ?? (() => 1),
		profile: opts.profile ?? (() => ({ name: 'Ana', avatar: { emoji: '🍌', color: 'banana' } })),
		location: opts.location ?? { protocol: 'http:', host: 'localhost:5173' },
		win,
		doc
	});

	const h: Harness = {
		session,
		sockets,
		calls,
		win,
		doc,
		replies,
		sock: () => sockets[sockets.length - 1],
		settle: async () => void (await vi.advanceTimersByTimeAsync(0)),
		advance: async (ms) => void (await vi.advanceTimersByTimeAsync(ms)),
		join: async (view = lobbyView(), serverTime = Date.now()) => {
			await h.settle();
			const s = h.sock();
			s.open();
			s.state(view, serverTime);
			return s;
		},
		roomCalls: () => calls.filter((c) => c.url.startsWith('/api/rooms/')).length
	};
	return h;
}
