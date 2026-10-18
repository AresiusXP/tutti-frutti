import type { Account, AccountUser } from './store.svelte';
import { OK, type Outcome, type Stats } from './types';

/**
 * Scriptable stand-in for the account store (component tests and screenshots):
 * set the public fields, read the recorded calls, make an action pending or fail.
 */
export class FakeAccount implements Account {
	status = $state<'loading' | 'ready'>('ready');
	googleEnabled = $state(true);
	loggedIn = $state(false);
	user = $state.raw<AccountUser | null>(null);
	initError = $state<string | null>(null);
	stats = $state.raw<Stats | null>(null);
	statsState = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
	statsError = $state<string | null>(null);

	initCalls = 0;
	loginCalls: string[] = [];
	logoutCalls = 0;
	deleteCalls = 0;
	loadStatsCalls = 0;
	saveProfileCalls = 0;

	/** Override to script an action (e.g. a pending promise). The default is a successful one. */
	onLogout: () => Promise<Outcome> = () => Promise.resolve(OK);
	onDelete: () => Promise<Outcome> = () => Promise.resolve(OK);
	onLoadStats: () => Promise<Outcome> = () => Promise.resolve(OK);

	/** Back to a pristine guest (Google on); call it in `beforeEach`. */
	reset() {
		this.status = 'ready';
		this.googleEnabled = true;
		this.loggedIn = false;
		this.user = null;
		this.initError = null;
		this.stats = null;
		this.statsState = 'idle';
		this.statsError = null;
		this.initCalls = this.logoutCalls = this.deleteCalls = this.loadStatsCalls = 0;
		this.saveProfileCalls = 0;
		this.loginCalls = [];
		this.onLogout = this.onDelete = this.onLoadStats = () => Promise.resolve(OK);
	}

	/** Convenience: sign in as `user` (no stats loaded yet). */
	signIn(user: AccountUser = { id: 'u1', name: 'Ana García', profile: null }) {
		this.loggedIn = true;
		this.user = user;
	}

	init() {
		this.initCalls++;
		return Promise.resolve();
	}

	login(returnTo: string = '/') {
		this.loginCalls.push(returnTo);
	}

	async logout() {
		this.logoutCalls++;
		const outcome = await this.onLogout();
		if (outcome.ok) this.#guest();
		return outcome;
	}

	async deleteAccount() {
		this.deleteCalls++;
		const outcome = await this.onDelete();
		if (outcome.ok) this.#guest();
		return outcome;
	}

	async loadStats() {
		this.loadStatsCalls++;
		return this.onLoadStats();
	}

	saveProfile() {
		this.saveProfileCalls++;
		return Promise.resolve(OK);
	}

	#guest() {
		this.loggedIn = false;
		this.user = null;
		this.stats = null;
		this.statsState = 'idle';
	}
}
