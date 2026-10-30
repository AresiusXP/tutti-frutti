/**
 * Terms acceptance of this browser identity (docs/DESIGN.md, "Content safety" and "Terms gate").
 *
 * The server asks for an accepted version of the terms before a guest may create or join a room or
 * upload a profile. This store holds what `GET /api/session` said (`terms`), opens the gate sheet
 * (`TermsGate`, mounted once in the layout) when an *intent* needs the acceptance, and hands the
 * answer back to whoever was waiting, so the interrupted action (create, join) simply continues.
 *
 *   if (!(await terms.ensure())) return;          // before the intent
 *   await terms.run(() => api.createRoom(...));   // before + once again after a 403 terms_required
 *
 * Reporting, blocking, leaving, signing out and deleting the account never call this.
 *
 * An unknown state (the session could not be loaded) never blocks the client: the server fails
 * closed for every upload, and its `terms_required` answer comes back here (`run`, the room route).
 */
import { createAccountApi } from '$lib/account/api';
import { ApiError } from '$lib/api';
import { i18n } from '$lib/i18n.svelte';
import { historySettled } from '$lib/lobby/historyPop';
import type { TermsStatus } from '$lib/protocol/api';
import { tick } from 'svelte';
import { type SafetyApi, safetyApi } from './api';

/** What `terms.run` returns when the player closed the gate instead of accepting. */
export const DECLINED = Symbol('terms declined');

export interface TermsDeps {
	api?: SafetyApi;
	/** `GET /api/session` (the guest cookie is minted here): the account client's by default. */
	loadSession?: () => Promise<{ terms: TermsStatus }>;
	/** The screen language the terms are shown in (es, en or nl). */
	locale?: () => string;
}

const codeOf = (e: unknown): string => (e instanceof ApiError ? e.code : 'unknown');

export class TermsStore {
	/** `unknown` until a session answered (or failed to); the gate only ever opens from `ready`. */
	status = $state<'unknown' | 'ready'>('unknown');
	requiredVersion = $state('');
	acceptedVersion = $state<string | null>(null);
	acceptanceRequired = $state(false);

	/** The gate sheet is showing. */
	open = $state(false);
	/** `POST /api/terms/accept` is in flight. */
	saving = $state(false);
	/** Error code of the last failed acceptance (network, server_busy, ...), else `null`. */
	error = $state<string | null>(null);
	/** The version changed while the gate was open: the player must read and accept again. */
	updated = $state(false);

	#deps: TermsDeps;
	#waiters: Array<(accepted: boolean) => void> = [];
	#refreshing: Promise<boolean> | null = null;

	constructor(deps: TermsDeps = {}) {
		this.#deps = deps;
	}

	/** True while uploads would be refused: the profile sync waits for this to turn false. */
	get blocksUploads(): boolean {
		return this.status === 'ready' && this.acceptanceRequired;
	}

	/** Takes what the server said (`GET /api/session`, the answer of `POST /api/terms/accept`). */
	apply(terms: TermsStatus): void {
		this.status = 'ready';
		this.requiredVersion = terms.requiredVersion;
		this.acceptedVersion = terms.acceptedVersion;
		this.acceptanceRequired = terms.acceptanceRequired;
		if (!terms.acceptanceRequired) this.#settle(true);
	}

	/** Loads `GET /api/session` (shared by concurrent callers). `false` when it could not be loaded. */
	refresh(): Promise<boolean> {
		if (this.#refreshing) return this.#refreshing;
		const run = (async () => {
			try {
				const load = this.#deps.loadSession ?? (() => createAccountApi().session());
				this.apply((await load()).terms);
				return true;
			} catch {
				return false;
			}
		})().finally(() => {
			this.#refreshing = null;
		});
		this.#refreshing = run;
		return run;
	}

	/**
	 * Resolves `true` when the terms are accepted (at once when they already are), `false` when the
	 * player closed the gate. While the state is unknown it asks the server once; if that fails it
	 * lets the intent go ahead (the server decides, and answers `terms_required` when it must).
	 */
	async ensure(): Promise<boolean> {
		if (this.status === 'unknown' && !(await this.refresh())) return true;
		if (!this.acceptanceRequired) return true;
		return this.#openGate();
	}

	/** The server said `terms_required` (a new version, an expired acceptance): the gate must show again. */
	require(): void {
		this.status = 'ready';
		this.acceptanceRequired = true;
	}

	/**
	 * Runs `action` once the terms are accepted. A `terms_required` answer (this client's idea of the
	 * state was stale) opens the gate and, after the acceptance, runs the action ONCE more; a second
	 * refusal is the caller's to show. `DECLINED` when the player closed the gate.
	 */
	async run<T>(action: () => Promise<T>): Promise<T | typeof DECLINED> {
		if (!(await this.ensure())) return DECLINED;
		try {
			return await action();
		} catch (e) {
			if (!(e instanceof ApiError) || e.code !== 'terms_required') throw e;
			this.require();
			if (!(await this.ensure())) return DECLINED;
			return await action();
		}
	}

	/** The gate's Accept button: the player ticked "13 or older and accept". */
	async accept(): Promise<void> {
		if (this.saving || !this.open) return;
		this.saving = true;
		this.error = null;
		try {
			this.apply(await this.#send());
			this.updated = false;
		} catch (e) {
			const code = codeOf(e);
			if (code === 'terms_version_mismatch') {
				// A new version was deployed while the gate was open: show it again with the new number.
				this.updated = true;
				if (!(await this.refresh())) this.error = 'network';
			} else this.error = code;
		} finally {
			this.saving = false;
		}
	}

	async #send(): Promise<TermsStatus> {
		const request = () =>
			(this.#deps.api ?? safetyApi).acceptTerms({
				version: this.requiredVersion,
				locale: this.#deps.locale?.() ?? i18n.locale,
				age13Confirmed: true
			});
		try {
			return await request();
		} catch (e) {
			// No guest cookie (cleared meanwhile): the session call mints one; then ask again.
			if (!(e instanceof ApiError) || e.status !== 401 || !(await this.refresh())) throw e;
			return await request();
		}
	}

	/** The gate's Not now / close: whoever waits is told `false` and the sheet goes away. */
	dismiss(): void {
		this.error = null;
		this.updated = false;
		this.#settle(false);
	}

	#openGate(): Promise<boolean> {
		this.open = true;
		return new Promise((resolve) => this.#waiters.push(resolve));
	}

	/**
	 * Closes the gate and tells whoever waited. The answer comes once the sheet is gone AND the history
	 * entry it pushed is popped: the waiter usually navigates next (a created room), and a pop that
	 * arrives after the navigation started would cancel it (see `historySettled`).
	 */
	#settle(accepted: boolean): void {
		this.open = false;
		const waiters = this.#waiters.splice(0);
		if (waiters.length === 0) return;
		void tick()
			.then(historySettled)
			.then(() => {
				for (const resolve of waiters) resolve(accepted);
			});
	}
}

export const terms = new TermsStore();
