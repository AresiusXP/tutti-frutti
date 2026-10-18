/**
 * Account state of the page: who (if anybody) is signed in, their saved
 * profile and lifetime stats, plus the profile sync.
 *
 * A guest never notices any of this: `init()` is one `GET /api/session`, and
 * nothing else runs (no listeners, no timers) until the session says
 * `loggedIn`. Network trouble is never an error the UI has to handle: the app
 * stays a working guest app and `init()` is retried lazily (`online`, tab
 * visible, next `init()` call).
 *
 * Profile sync (logged in only, see docs/DESIGN.md "Accounts UI"):
 *  - the server's saved profile wins once per page load (nickname + avatar are
 *    adopted into the local `profile` store) — unless the user already typed
 *    something while the request was in flight;
 *  - no saved profile yet (first login): the local nickname is uploaded, or the
 *    Google display name becomes the nickname and is uploaded;
 *  - afterwards every nickname/avatar change is saved with a trailing debounce,
 *    flushed when the tab is hidden / the page is left. Failures are silent
 *    (the next change retries) except one calm toast after repeated failures.
 */
import { untrack } from 'svelte';
import { ApiError } from '$lib/api';
import { isAvatar } from '$lib/avatar';
import { m } from '$lib/paraglide/messages';
import { type ProfileStore, profile as sharedProfile } from '$lib/profile.svelte';
import { toasts } from '$lib/toast.svelte';
import { type AccountApi, createAccountApi } from './api';
import { loginPath } from './returnTo';
import { OK, type Outcome, type SavedProfile, type Stats } from './types';

export const PROFILE_SAVE_DEBOUNCE_MS = 800;
/** Consecutive failed saves before the user is told (once per outage). */
export const SAVE_FAILURES_BEFORE_TOAST = 3;

export interface AccountUser {
	id: string;
	/** Google display name (≤ 16 runes). */
	name: string;
	/** What the server has saved; `null` until the first sync. */
	profile: SavedProfile | null;
}

type Timer = ReturnType<typeof setTimeout>;

export interface AccountDeps {
	/** Used to build the REST client; defaults to the global `fetch` (looked up on every call). */
	fetch?: (input: string, init?: RequestInit) => Promise<Response>;
	api?: AccountApi;
	profile?: ProfileStore;
	setTimeout?: (fn: () => void, ms: number) => Timer;
	clearTimeout?: (timer: Timer) => void;
	/** Full-page navigation (login); defaults to `location.assign`. */
	navigate?: (url: string) => void;
	/** Shows a message to the user; defaults to the toast queue. */
	notify?: (text: string) => void;
	win?: Pick<Window, 'addEventListener' | 'removeEventListener'> | null;
	doc?: Pick<Document, 'addEventListener' | 'removeEventListener' | 'visibilityState'> | null;
}

const failure = (code: string): Outcome => ({ ok: false, code });
const codeOf = (e: unknown): string => (e instanceof ApiError ? e.code : 'unknown');

function snapshotKey(nickname: string, avatar: { emoji: string; color: string }): string {
	return JSON.stringify([nickname, avatar.emoji, avatar.color]);
}

export class AccountStore {
	/** `loading` until the first `/api/session` answered (or failed). */
	status = $state<'loading' | 'ready'>('loading');
	googleEnabled = $state(false);
	loggedIn = $state(false);
	user = $state.raw<AccountUser | null>(null);
	/** Why the last `init()` could not finish (a network/server error code), else `null`. */
	initError = $state<string | null>(null);

	stats = $state.raw<Stats | null>(null);
	statsState = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
	statsError = $state<string | null>(null);

	#api: AccountApi;
	#profile: ProfileStore;
	#setTimeout: NonNullable<AccountDeps['setTimeout']>;
	#clearTimeout: NonNullable<AccountDeps['clearTimeout']>;
	#navigate: (url: string) => void;
	#notify: (text: string) => void;
	#win: AccountDeps['win'];
	#doc: AccountDeps['doc'];

	#initPromise: Promise<void> | null = null;
	#baseline: string | null = null;
	#adopted = false;
	#statsSeq = 0;

	#synced: string | null = null;
	#timer: Timer | null = null;
	#inflight: Promise<Outcome> | null = null;
	#inflightKey: string | null = null;
	#again = false;
	#failures = 0;
	#warned = false;
	#stopWatch: (() => void) | null = null;
	#unlisten: Array<() => void> = [];
	#unlistenRetry: Array<() => void> = [];

	constructor(deps: AccountDeps = {}) {
		this.#api = deps.api ?? createAccountApi(deps.fetch);
		this.#profile = deps.profile ?? sharedProfile;
		this.#setTimeout = deps.setTimeout ?? ((fn, ms) => globalThis.setTimeout(fn, ms));
		this.#clearTimeout = deps.clearTimeout ?? ((timer) => globalThis.clearTimeout(timer));
		this.#navigate = deps.navigate ?? ((url) => location.assign(url));
		this.#notify = deps.notify ?? ((text) => toasts.show(text));
		this.#win = deps.win === undefined ? (typeof window === 'undefined' ? null : window) : deps.win;
		this.#doc =
			deps.doc === undefined ? (typeof document === 'undefined' ? null : document) : deps.doc;
	}

	// ---- session ---------------------------------------------------------------------------

	/**
	 * Loads the session (and, when logged in, the user) once per page load; later calls get the
	 * same promise. After a network/server failure the next call tries again. Never rejects.
	 */
	init(): Promise<void> {
		if (this.#initPromise) return this.#initPromise;
		this.#baseline ??= this.#key();
		const run = this.#load().finally(() => {
			if (this.initError !== null) this.#initPromise = null;
		});
		this.#initPromise = run;
		return run;
	}

	async #load(): Promise<void> {
		try {
			const session = await this.#api.session();
			this.googleEnabled = session.googleEnabled;
			this.loggedIn = session.loggedIn;
			if (!session.loggedIn) this.#reset();
			else await this.#loadMe();
			this.initError = null;
		} catch (e) {
			this.initError = codeOf(e);
		} finally {
			this.status = 'ready';
		}
		if (this.initError === null) this.#disarmRetry();
		else this.#armRetry();
	}

	async #loadMe(): Promise<void> {
		let me;
		try {
			me = await this.#api.me();
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				this.#reset(); // the user no longer exists or the cookie is stale: a guest again
				return;
			}
			throw e;
		}
		this.user = { id: me.id, name: me.name, profile: me.profile };
		if (!this.#adopted) {
			this.#adopted = true;
			this.#reconcile(me.name, me.profile);
		}
		this.#watch();
	}

	/** First `/api/me` of the page: decide which side wins (see the file header). */
	#reconcile(name: string, saved: SavedProfile | null) {
		const edited = this.#key() !== this.#baseline;
		if (saved && !edited) {
			this.#profile.setNickname(saved.nickname);
			if (isAvatar(saved.avatar)) this.#profile.setAvatar(saved.avatar); // a foreign palette keeps the local one
			this.#synced = this.#key();
			return;
		}
		// No saved profile (first login), or the user typed before the answer arrived: local wins.
		this.#synced = saved ? snapshotKey(saved.nickname, saved.avatar) : null;
		if (!this.#profile.nickname && !edited) this.#profile.setNickname(name);
		void this.saveProfile();
	}

	/** Full-page navigation to the Google login; `returnTo` is `/` or `/<ROOMCODE>` (else `/`). */
	login(returnTo: string = '/') {
		this.#navigate(loginPath(returnTo));
	}

	async logout(): Promise<Outcome> {
		await this.saveProfile(); // do not lose an edit made in the last 800 ms
		while (this.#inflight) await this.#inflight; // ... nor one queued behind a save in flight
		try {
			await this.#api.logout();
		} catch (e) {
			return failure(codeOf(e));
		}
		this.#reset();
		return OK;
	}

	async deleteAccount(): Promise<Outcome> {
		try {
			await this.#api.deleteMe();
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) this.#reset();
			return failure(codeOf(e));
		}
		this.#reset();
		return OK;
	}

	/** Back to a guest (keeps `googleEnabled`): stops the sync and forgets everything account related. */
	#reset() {
		this.#clearDebounce();
		this.#stopWatching();
		this.loggedIn = false;
		this.user = null;
		this.stats = null;
		this.statsState = 'idle';
		this.statsError = null;
		this.#statsSeq++;
		this.#adopted = false;
		this.#synced = null;
		this.#again = false;
		this.#failures = 0;
		this.#warned = false;
	}

	// ---- stats -----------------------------------------------------------------------------

	/** Fetches the lifetime stats. Keeps showing the previous numbers while refreshing. */
	async loadStats(): Promise<Outcome> {
		if (!this.loggedIn) return failure('unauthorized');
		const mine = ++this.#statsSeq;
		if (!this.stats) this.statsState = 'loading';
		this.statsError = null;
		try {
			const stats = await this.#api.stats();
			if (mine !== this.#statsSeq) return OK;
			this.stats = stats;
			this.statsState = 'ready';
			return OK;
		} catch (e) {
			const code = codeOf(e);
			if (mine !== this.#statsSeq) return failure(code);
			if (e instanceof ApiError && e.status === 401) this.#reset();
			else {
				this.statsState = 'error';
				this.statsError = code;
			}
			return failure(code);
		}
	}

	// ---- profile sync ----------------------------------------------------------------------

	#key(): string {
		return snapshotKey(this.#profile.nickname, this.#profile.avatar);
	}

	/**
	 * Uploads the local nickname + avatar now when they differ from what the server has.
	 * Nothing to do without a nickname (the server rejects an empty one; the user is typing).
	 */
	saveProfile(opts: { keepalive?: boolean } = {}): Promise<Outcome> {
		this.#clearDebounce();
		if (!this.loggedIn) return Promise.resolve(failure('unauthorized'));
		const { nickname, avatar } = this.#profile;
		const key = this.#key();
		if (!nickname || key === this.#synced) return Promise.resolve(OK);
		if (this.#inflight) {
			this.#again = true;
			return this.#inflight;
		}
		this.#inflightKey = key;
		const run = (async (): Promise<Outcome> => {
			try {
				await this.#api.saveProfile({ nickname, avatar: { ...avatar } }, opts);
				this.#synced = key;
				this.#failures = 0;
				this.#warned = false;
				return OK;
			} catch (e) {
				if (e instanceof ApiError && e.status === 401) {
					this.#reset();
					return failure('unauthorized');
				}
				this.#failures++;
				if (this.#failures >= SAVE_FAILURES_BEFORE_TOAST && !this.#warned) {
					this.#warned = true;
					this.#notify(m.account_save_failed());
				}
				return failure(codeOf(e));
			} finally {
				this.#inflight = null;
				this.#inflightKey = null;
				if (this.#again) {
					this.#again = false;
					void this.saveProfile();
				}
			}
		})();
		this.#inflight = run;
		return run;
	}

	#clearDebounce() {
		if (this.#timer === null) return;
		this.#clearTimeout(this.#timer);
		this.#timer = null;
	}

	#onLocalChange(key: string) {
		if (key === this.#synced || key === this.#inflightKey) return;
		this.#clearDebounce();
		this.#timer = this.#setTimeout(() => {
			this.#timer = null;
			void this.saveProfile();
		}, PROFILE_SAVE_DEBOUNCE_MS);
	}

	/** Starts following the local profile (idempotent); only ever runs for a logged-in user. */
	#watch() {
		if (this.#stopWatch) return;
		const profile = this.#profile;
		this.#stopWatch = $effect.root(() => {
			$effect(() => {
				const key = snapshotKey(profile.nickname, profile.avatar);
				untrack(() => this.#onLocalChange(key));
			});
		});
		const flush = () => void this.saveProfile({ keepalive: true });
		this.#listen(this.#unlisten, this.#doc, 'visibilitychange', () => {
			if (this.#doc?.visibilityState === 'hidden') flush();
		});
		this.#listen(this.#unlisten, this.#win, 'pagehide', flush);
	}

	#stopWatching() {
		this.#stopWatch?.();
		this.#stopWatch = null;
		for (const off of this.#unlisten.splice(0)) off();
	}

	// ---- lazy retry ------------------------------------------------------------------------

	#armRetry() {
		if (this.#unlistenRetry.length > 0) return;
		const retry = () => {
			if (this.initError !== null) void this.init();
		};
		this.#listen(this.#unlistenRetry, this.#win, 'online', retry);
		this.#listen(this.#unlistenRetry, this.#doc, 'visibilitychange', () => {
			if (this.#doc?.visibilityState === 'visible') retry();
		});
	}

	#disarmRetry() {
		for (const off of this.#unlistenRetry.splice(0)) off();
	}

	#listen(
		into: Array<() => void>,
		target: Pick<EventTarget, 'addEventListener' | 'removeEventListener'> | null | undefined,
		type: string,
		handler: () => void
	) {
		if (!target) return;
		target.addEventListener(type, handler);
		into.push(() => target.removeEventListener(type, handler));
	}
}

/** The shape components and test doubles rely on (public members only). */
export type Account = { [K in keyof AccountStore]: AccountStore[K] };

export const account: Account = new AccountStore();
