/** Test doubles for the account feature: a routable fake `fetch`, event targets and a memory profile. */
import { vi } from 'vitest';
import { type StorageLike, ProfileStore } from '$lib/profile.svelte';
import { TermsStore } from '$lib/safety/terms.svelte';
import { AccountStore, type AccountDeps } from './store.svelte';

export const json = (body: unknown, status = 200, headers: Record<string, string> = {}): Response =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json', ...headers }
	});

export const empty = (status = 204): Response => new Response(null, { status });

export interface Call {
	key: string;
	method: string;
	path: string;
	init: RequestInit;
	/** The parsed JSON body, if any. */
	body: unknown;
}

export type Reply = Response | Error;
export type Handler = Reply | ((call: Call) => Reply | Promise<Reply>);

/** `new FakeServer().on('GET /api/session', json(...))`: unrouted requests answer 404 and are listed in `unrouted`. */
export class FakeServer {
	calls: Call[] = [];
	unrouted: string[] = [];
	#routes = new Map<string, Handler>();

	on(key: string, handler: Handler): this {
		this.#routes.set(key, handler);
		return this;
	}

	callsTo(key: string): Call[] {
		return this.calls.filter((c) => c.key === key);
	}

	get keys(): string[] {
		return this.calls.map((c) => c.key);
	}

	fetch = vi.fn(async (input: string, init: RequestInit = {}): Promise<Response> => {
		const method = (init.method ?? 'GET').toUpperCase();
		const path = new URL(input, 'http://localhost').pathname;
		const key = `${method} ${path}`;
		const body = typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined;
		const call: Call = { key, method, path, init, body };
		this.calls.push(call);
		const handler = this.#routes.get(key);
		if (!handler) {
			this.unrouted.push(key);
			return json({ error: 'not_found' }, 404);
		}
		const reply = typeof handler === 'function' ? await handler(call) : handler;
		if (reply instanceof Error) throw reply;
		return reply.clone();
	});
}

export function deferred<T>() {
	let resolve!: (v: T) => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

type Listener = () => void;

/** A minimal `EventTarget` that records listeners so tests can fire events and count leaks. */
export class FakeTarget {
	visibilityState: DocumentVisibilityState = 'visible';
	#listeners = new Map<string, Set<Listener>>();

	addEventListener(type: string, fn: Listener) {
		if (!this.#listeners.has(type)) this.#listeners.set(type, new Set());
		this.#listeners.get(type)!.add(fn);
	}

	removeEventListener(type: string, fn: Listener) {
		this.#listeners.get(type)?.delete(fn);
	}

	fire(type: string) {
		for (const fn of [...(this.#listeners.get(type) ?? [])]) fn();
	}

	get count(): number {
		return [...this.#listeners.values()].reduce((n, set) => n + set.size, 0);
	}
}

export function memoryProfile(
	nickname = '',
	avatar = { emoji: '🍌', color: 'banana' }
): ProfileStore {
	const data: Record<string, string> = {
		'tf.nickname': nickname,
		'tf.avatar': JSON.stringify(avatar)
	};
	const storage: StorageLike = {
		getItem: (k) => data[k] ?? null,
		setItem: (k, v) => {
			data[k] = v;
		}
	};
	return new ProfileStore(storage);
}

export const TERMS_ACCEPTED = {
	requiredVersion: '2026-10',
	acceptedVersion: '2026-10',
	acceptanceRequired: false
};
export const TERMS_PENDING = {
	requiredVersion: '2026-10',
	acceptedVersion: null,
	acceptanceRequired: true
};
export const SESSION_GUEST = {
	guest: true,
	googleEnabled: true,
	loggedIn: false,
	terms: TERMS_ACCEPTED
};
export const SESSION_IN = {
	guest: true,
	googleEnabled: true,
	loggedIn: true,
	terms: TERMS_ACCEPTED
};
export const ME = {
	id: 'u1',
	name: 'Ana García',
	profile: { nickname: 'Anita', avatar: { emoji: '🍓', color: 'strawberry' } }
};
export const ME_FIRST = { ...ME, profile: null };
export const STATS = {
	gamesPlayed: 12,
	wins: 5,
	totalPoints: 1240,
	bestRound: 80,
	uniqueAnswers: 37
};

export interface Rig {
	server: FakeServer;
	profile: ProfileStore;
	terms: TermsStore;
	store: AccountStore;
	win: FakeTarget;
	doc: FakeTarget;
	notify: ReturnType<typeof vi.fn<(text: string) => void>>;
	navigate: ReturnType<typeof vi.fn<(url: string) => void>>;
}

export function rig(
	opts: { nickname?: string; avatar?: { emoji: string; color: string } } = {},
	deps: Partial<AccountDeps> = {}
): Rig {
	const server = new FakeServer();
	const profile = memoryProfile(opts.nickname, opts.avatar);
	const terms = new TermsStore();
	const win = new FakeTarget();
	const doc = new FakeTarget();
	const notify = vi.fn<(text: string) => void>();
	const navigate = vi.fn<(url: string) => void>();
	const store = new AccountStore({
		fetch: server.fetch,
		profile,
		terms,
		win: win as unknown as AccountDeps['win'],
		doc: doc as unknown as AccountDeps['doc'],
		notify,
		navigate,
		...deps
	});
	return { server, profile, terms, store, win, doc, notify, navigate };
}
