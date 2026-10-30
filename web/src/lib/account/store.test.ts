// @vitest-environment jsdom
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { PROFILE_SAVE_DEBOUNCE_MS, SAVE_FAILURES_BEFORE_TOAST } from './store.svelte';
import {
	ME,
	ME_FIRST,
	SESSION_GUEST,
	SESSION_IN,
	STATS,
	TERMS_ACCEPTED,
	TERMS_PENDING,
	deferred,
	empty,
	json,
	rig,
	type Rig
} from './account.testkit';

const PUT = 'PUT /api/me/profile';

/** A signed-in rig: `/api/me` answers `me`, saves succeed. */
function signedIn(me: unknown = ME, opts: Parameters<typeof rig>[0] = {}): Rig {
	const r = rig(opts);
	r.server
		.on('GET /api/session', json(SESSION_IN))
		.on('GET /api/me', json(me))
		.on('GET /api/me/stats', json(STATS))
		.on(PUT, empty())
		.on('POST /api/auth/logout', empty())
		.on('DELETE /api/me', empty());
	return r;
}

const edit = async (r: Rig, nickname: string) => {
	r.profile.setNickname(nickname);
	await tick();
};

beforeEach(() => {
	i18n.set('es');
	vi.useFakeTimers();
});
afterEach(() => {
	vi.useRealTimers();
});

describe('guest', () => {
	it('loads the session once and stays out of the way', async () => {
		const r = rig();
		r.server.on('GET /api/session', json(SESSION_GUEST));
		expect(r.store.status).toBe('loading');
		await r.store.init();
		expect(r.store.status).toBe('ready');
		expect([r.store.googleEnabled, r.store.loggedIn, r.store.user, r.store.stats]).toEqual([
			true,
			false,
			null,
			null
		]);
		expect(r.store.initError).toBeNull();
		expect(r.server.keys).toEqual(['GET /api/session']);
	});

	it('Google not configured: googleEnabled is false', async () => {
		const r = rig();
		r.server.on('GET /api/session', json({ guest: true, googleEnabled: false, loggedIn: false }));
		await r.store.init();
		expect(r.store.googleEnabled).toBe(false);
		expect(r.store.loggedIn).toBe(false);
	});

	it('guests see zero behaviour change: no listeners, no timers, no uploads, whatever they edit', async () => {
		const r = rig({ nickname: 'Beto' });
		r.server.on('GET /api/session', json(SESSION_GUEST));
		await r.store.init();
		await edit(r, 'Beto 2');
		r.profile.setAvatar({ emoji: '🍉' });
		await tick();
		expect(vi.getTimerCount()).toBe(0);
		await vi.advanceTimersByTimeAsync(5000);
		r.doc.visibilityState = 'hidden';
		r.doc.fire('visibilitychange');
		r.win.fire('pagehide');
		expect(r.server.keys).toEqual(['GET /api/session']);
		expect(r.win.count + r.doc.count).toBe(0);
		expect(r.profile.nickname).toBe('Beto 2');
	});

	it('saveProfile() as a guest does nothing and says unauthorized', async () => {
		const r = rig({ nickname: 'Beto' });
		r.server.on('GET /api/session', json(SESSION_GUEST));
		await r.store.init();
		expect(await r.store.saveProfile()).toEqual({ ok: false, code: 'unauthorized' });
		expect(r.server.keys).toEqual(['GET /api/session']);
	});
});

describe('init', () => {
	it('is once per page load: concurrent and later calls share one request', async () => {
		const r = rig();
		const gate = deferred<Response>();
		r.server.on('GET /api/session', () => gate.promise);
		const first = r.store.init();
		const second = r.store.init();
		expect(second).toBe(first);
		gate.resolve(json(SESSION_GUEST));
		await Promise.all([first, second]);
		await r.store.init();
		expect(r.server.callsTo('GET /api/session')).toHaveLength(1);
	});

	it('logged in: session, then /api/me, in that order, with the user exposed', async () => {
		const r = signedIn();
		await r.store.init();
		expect(r.server.keys.slice(0, 2)).toEqual(['GET /api/session', 'GET /api/me']);
		expect(r.store.loggedIn).toBe(true);
		expect(r.store.user).toEqual({ id: 'u1', name: 'Ana García', profile: ME.profile });
		expect(r.store.status).toBe('ready');
	});

	it('stays loading until /api/me answered (no half-logged-in flicker)', async () => {
		const r = signedIn();
		const gate = deferred<Response>();
		r.server.on('GET /api/me', () => gate.promise);
		const init = r.store.init();
		await vi.advanceTimersByTimeAsync(10);
		expect(r.store.status).toBe('loading');
		gate.resolve(json(ME));
		await init;
		expect(r.store.status).toBe('ready');
	});

	it('401 on /api/me: treated as logged out (user gone / stale cookie)', async () => {
		const r = signedIn();
		r.server.on('GET /api/me', json({ error: 'unauthorized' }, 401));
		await r.store.init();
		expect([r.store.status, r.store.loggedIn, r.store.user, r.store.initError]).toEqual([
			'ready',
			false,
			null,
			null
		]);
		expect(r.store.googleEnabled).toBe(true);
		await edit(r, 'Otro');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		expect(r.win.count + r.doc.count).toBe(0);
	});

	it('network failure on the session: usable guest app, no throw, error recorded', async () => {
		const r = rig();
		r.server.on('GET /api/session', new TypeError('Failed to fetch'));
		await expect(r.store.init()).resolves.toBeUndefined();
		expect([r.store.status, r.store.loggedIn, r.store.googleEnabled, r.store.initError]).toEqual([
			'ready',
			false,
			false,
			'network'
		]);
	});

	it('a failed init is retried by the next init() call', async () => {
		const r = rig();
		r.server.on('GET /api/session', new TypeError('offline'));
		await r.store.init();
		r.server.on('GET /api/session', json(SESSION_GUEST));
		await r.store.init();
		expect(r.store.initError).toBeNull();
		expect(r.store.googleEnabled).toBe(true);
		expect(r.server.callsTo('GET /api/session')).toHaveLength(2);
	});

	it('retries lazily when the browser comes back online or the tab is shown again, then stops listening', async () => {
		const r = rig();
		r.server.on('GET /api/session', json({ error: 'server_busy' }, 503));
		await r.store.init();
		expect(r.store.initError).toBe('server_busy');
		expect(r.win.count + r.doc.count).toBe(2);

		r.doc.visibilityState = 'hidden';
		r.doc.fire('visibilitychange'); // hidden: no retry
		expect(r.server.callsTo('GET /api/session')).toHaveLength(1);

		r.server.on('GET /api/session', json(SESSION_GUEST));
		r.win.fire('online');
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo('GET /api/session')).toHaveLength(2);
		expect([r.store.initError, r.store.googleEnabled]).toEqual([null, true]);
		expect(r.win.count + r.doc.count).toBe(0);
		r.win.fire('online');
		expect(r.server.callsTo('GET /api/session')).toHaveLength(2);
	});

	it('the tab becoming visible also retries', async () => {
		const r = rig();
		r.server.on('GET /api/session', new TypeError('offline'));
		await r.store.init();
		r.server.on('GET /api/session', json(SESSION_GUEST));
		r.doc.visibilityState = 'visible';
		r.doc.fire('visibilitychange');
		await vi.advanceTimersByTimeAsync(1);
		expect(r.store.initError).toBeNull();
	});

	it('a failure of /api/me keeps the session as logged in, retries, and adopts the profile then', async () => {
		const r = signedIn();
		r.server.on('GET /api/me', new TypeError('offline'));
		await r.store.init();
		expect([r.store.loggedIn, r.store.user, r.store.initError]).toEqual([true, null, 'network']);
		expect(r.profile.nickname).toBe(''); // nothing adopted from a failed request

		r.server.on('GET /api/me', json(ME));
		await r.store.init();
		expect(r.store.user?.name).toBe('Ana García');
		expect(r.profile.nickname).toBe('Anita');
	});

	it('a malformed /api/me answer is an error, not a crash', async () => {
		const r = signedIn({ nonsense: true });
		await expect(r.store.init()).resolves.toBeUndefined();
		expect(r.store.initError).toBe('unknown');
		expect(r.store.loggedIn).toBe(true);
	});
});

describe('profile sync: first load', () => {
	it('the server wins: nickname and avatar are adopted, nothing is uploaded back', async () => {
		const r = signedIn(ME, { nickname: 'Local', avatar: { emoji: '🍌', color: 'banana' } });
		await r.store.init();
		await tick();
		expect(r.profile.nickname).toBe('Anita');
		expect(r.profile.avatar).toEqual({ emoji: '🍓', color: 'strawberry' });
		expect(vi.getTimerCount()).toBe(0); // adopting is not a change to save
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
	});

	it('a saved avatar outside the palette keeps the local avatar but adopts the nickname', async () => {
		const r = signedIn(
			{ ...ME, profile: { nickname: 'Anita', avatar: { emoji: '👾', color: 'pink' } } },
			{ avatar: { emoji: '🍊', color: 'orange' } }
		);
		await r.store.init();
		expect(r.profile.nickname).toBe('Anita');
		expect(r.profile.avatar).toEqual({ emoji: '🍊', color: 'orange' });
	});

	it('first login with a local nickname: uploads it, exactly once', async () => {
		const r = signedIn(ME_FIRST, { nickname: 'Beto', avatar: { emoji: '🍉', color: 'lime' } });
		await r.store.init();
		await tick();
		await vi.advanceTimersByTimeAsync(5000);
		const puts = r.server.callsTo(PUT);
		expect(puts).toHaveLength(1);
		expect(puts[0].body).toEqual({ nickname: 'Beto', avatar: { emoji: '🍉', color: 'lime' } });
		expect(puts[0].init.credentials).toBe('same-origin');
		expect(r.profile.nickname).toBe('Beto');
	});

	it('first login without a nickname: prefills it with the Google name and uploads it', async () => {
		const r = signedIn(
			{ ...ME_FIRST, name: 'María del Carmen' },
			{ nickname: '', avatar: { emoji: '🍇', color: 'grape' } }
		);
		await r.store.init();
		await tick();
		expect(r.profile.nickname).toBe('María del Carmen');
		expect(r.server.callsTo(PUT)[0].body).toEqual({
			nickname: 'María del Carmen',
			avatar: { emoji: '🍇', color: 'grape' }
		});
	});

	it('first login with a Google name that cannot be a nickname: nothing is uploaded', async () => {
		const r = signedIn({ ...ME_FIRST, name: '   ' }, { nickname: '' });
		await r.store.init();
		await tick();
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.profile.nickname).toBe('');
		expect(r.server.callsTo(PUT)).toHaveLength(0);
	});

	it('first login: a failed initial upload is retried by the next change', async () => {
		const r = signedIn(ME_FIRST, { nickname: 'Beto' });
		r.server.on(PUT, json({ error: 'internal' }, 500));
		await r.store.init();
		await tick();
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		r.server.on(PUT, empty());
		await edit(r, 'Beto B');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT).map((c) => (c.body as { nickname: string }).nickname)).toEqual([
			'Beto',
			'Beto B'
		]);
	});

	it('what the user typed while /api/me was in flight wins over the saved profile and is uploaded', async () => {
		const r = signedIn(ME, { nickname: '' });
		const gate = deferred<Response>();
		r.server.on('GET /api/me', () => gate.promise);
		const init = r.store.init();
		await vi.advanceTimersByTimeAsync(5);
		r.profile.setNickname('Escribiendo');
		gate.resolve(json(ME));
		await init;
		await tick();
		expect(r.profile.nickname).toBe('Escribiendo');
		expect(r.server.callsTo(PUT).map((c) => c.body)).toEqual([
			{ nickname: 'Escribiendo', avatar: { emoji: '🍌', color: 'banana' } }
		]);
	});
});

describe('profile sync: following changes', () => {
	async function synced(): Promise<Rig> {
		const r = signedIn();
		await r.store.init();
		await tick();
		return r;
	}

	it('saves a change after 800 ms of quiet, once, with the latest value (trailing debounce)', async () => {
		const r = await synced();
		await edit(r, 'A');
		await vi.advanceTimersByTimeAsync(500);
		await edit(r, 'Ab');
		await vi.advanceTimersByTimeAsync(500);
		await edit(r, 'Abc');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS - 1);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.server.callsTo(PUT)[0].body).toEqual({
			nickname: 'Abc',
			avatar: { emoji: '🍓', color: 'strawberry' }
		});
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
	});

	it('avatar changes are saved too', async () => {
		const r = await synced();
		r.profile.setAvatar({ emoji: '🍍', color: 'lime' });
		await tick();
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT)[0].body).toEqual({
			nickname: 'Anita',
			avatar: { emoji: '🍍', color: 'lime' }
		});
	});

	it('does not send a change that went back to what the server has', async () => {
		const r = await synced();
		await edit(r, 'Otro');
		await vi.advanceTimersByTimeAsync(300);
		await edit(r, 'Anita');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
	});

	it('never uploads an empty nickname while the user is retyping it', async () => {
		const r = await synced();
		await edit(r, '');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		await edit(r, 'Nuevo');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
	});

	it('typing is never blocked by a slow save; a change during a save is sent right after, in order', async () => {
		const r = await synced();
		const gate = deferred<Response>();
		r.server.on(PUT, (call) =>
			call.body && (call.body as { nickname: string }).nickname === 'Uno' ? gate.promise : empty()
		);
		await edit(r, 'Uno');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		await edit(r, 'Dos'); // while "Uno" is still in flight
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT)).toHaveLength(1); // not concurrent
		gate.resolve(empty());
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo(PUT).map((c) => (c.body as { nickname: string }).nickname)).toEqual([
			'Uno',
			'Dos'
		]);
	});

	it('flushes at once when the tab is hidden, with keepalive so it survives the page', async () => {
		const r = await synced();
		await edit(r, 'Rapido');
		r.doc.visibilityState = 'hidden';
		r.doc.fire('visibilitychange');
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.server.callsTo(PUT)[0].init.keepalive).toBe(true);
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(1); // the debounce timer was cancelled
	});

	it('flushes on pagehide; a tab becoming visible, or nothing pending, sends nothing', async () => {
		const r = await synced();
		r.doc.visibilityState = 'visible';
		r.doc.fire('visibilitychange');
		r.win.fire('pagehide');
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		await edit(r, 'Chau');
		r.win.fire('pagehide');
		await vi.advanceTimersByTimeAsync(1);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.server.callsTo(PUT)[0].init.keepalive).toBe(true);
	});

	it('failures are silent, the next change retries, and one calm toast comes after repeated failures', async () => {
		const r = await synced();
		r.server.on(PUT, json({ error: 'internal' }, 500));
		for (let i = 1; i <= SAVE_FAILURES_BEFORE_TOAST + 2; i++) {
			await edit(r, `Intento ${i}`);
			await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
			expect(r.notify).toHaveBeenCalledTimes(i < SAVE_FAILURES_BEFORE_TOAST ? 0 : 1);
		}
		expect(r.server.callsTo(PUT)).toHaveLength(SAVE_FAILURES_BEFORE_TOAST + 2);
		expect(r.notify).toHaveBeenCalledWith(expect.stringContaining('perfil'));
	});

	it('a success ends the outage: a later outage may toast again', async () => {
		const r = await synced();
		r.server.on(PUT, new TypeError('offline'));
		for (let i = 0; i < SAVE_FAILURES_BEFORE_TOAST; i++) {
			await edit(r, `Mal ${i}`);
			await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		}
		expect(r.notify).toHaveBeenCalledTimes(1);
		r.server.on(PUT, empty());
		await edit(r, 'Bien');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		r.server.on(PUT, new TypeError('offline'));
		for (let i = 0; i < SAVE_FAILURES_BEFORE_TOAST; i++) {
			await edit(r, `Otra vez ${i}`);
			await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		}
		expect(r.notify).toHaveBeenCalledTimes(2);
	});

	it('an expired session (401 on save) ends the sync: guest again, no more requests', async () => {
		const r = await synced();
		r.server.on(PUT, json({ error: 'unauthorized' }, 401));
		await edit(r, 'Tarde');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.store.loggedIn).toBe(false);
		expect(r.store.user).toBeNull();
		expect(r.notify).not.toHaveBeenCalled();
		await edit(r, 'Mas tarde');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.win.count + r.doc.count).toBe(0);
	});
});

describe('login', () => {
	it('navigates (full page) to the login endpoint with a sanitised returnTo', () => {
		const r = rig();
		r.store.login();
		r.store.login('/');
		r.store.login('/kt7p');
		r.store.login('//evil.example');
		r.store.login('https://evil.example');
		r.store.login('/KT7');
		expect(r.navigate.mock.calls.map((c) => c[0])).toEqual([
			'/api/auth/google/login?returnTo=%2F',
			'/api/auth/google/login?returnTo=%2F',
			'/api/auth/google/login?returnTo=%2FKT7P',
			'/api/auth/google/login?returnTo=%2F',
			'/api/auth/google/login?returnTo=%2F',
			'/api/auth/google/login?returnTo=%2F'
		]);
		expect(r.server.calls).toHaveLength(0); // it is a navigation, not a fetch
	});
});

describe('logout', () => {
	it('POSTs, then resets to a guest (Google stays available) and stops syncing', async () => {
		const r = signedIn();
		await r.store.init();
		await r.store.loadStats();
		expect(await r.store.logout()).toEqual({ ok: true });
		expect(r.server.callsTo('POST /api/auth/logout')).toHaveLength(1);
		expect(r.server.callsTo('POST /api/auth/logout')[0].init.credentials).toBe('same-origin');
		expect([r.store.loggedIn, r.store.user, r.store.stats, r.store.statsState]).toEqual([
			false,
			null,
			null,
			'idle'
		]);
		expect(r.store.googleEnabled).toBe(true);
		await edit(r, 'Despues');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		expect(r.win.count + r.doc.count).toBe(0);
		expect(r.profile.nickname).toBe('Despues'); // the local profile stays the guest's
	});

	it('saves a pending edit before signing out', async () => {
		const r = signedIn();
		await r.store.init();
		await edit(r, 'Ultimo cambio');
		await r.store.logout();
		expect(r.server.keys.slice(-2)).toEqual([PUT, 'POST /api/auth/logout']);
		expect(r.server.callsTo(PUT)[0].body).toMatchObject({ nickname: 'Ultimo cambio' });
	});

	it('waits for a save in flight and the edit queued behind it before signing out', async () => {
		const r = signedIn();
		await r.store.init();
		const first = deferred<Response>();
		const second = deferred<Response>();
		r.server.on(PUT, (call) =>
			(call.body as { nickname: string }).nickname === 'Uno' ? first.promise : second.promise
		);
		await edit(r, 'Uno');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		await edit(r, 'Dos'); // queued behind "Uno"
		const out = r.store.logout();
		first.resolve(empty());
		await vi.advanceTimersByTimeAsync(10);
		expect(r.server.callsTo(PUT)).toHaveLength(2);
		expect(r.server.callsTo('POST /api/auth/logout')).toHaveLength(0); // "Dos" is not acknowledged yet
		second.resolve(empty());
		await out;
		expect(r.server.keys.slice(-3)).toEqual([PUT, PUT, 'POST /api/auth/logout']);
	});

	it('a failure keeps the user signed in and reports the code', async () => {
		const r = signedIn();
		await r.store.init();
		r.server.on('POST /api/auth/logout', new TypeError('offline'));
		expect(await r.store.logout()).toEqual({ ok: false, code: 'network' });
		expect(r.store.loggedIn).toBe(true);
		expect(r.store.user).not.toBeNull();
	});
});

describe('deleteAccount', () => {
	it('DELETEs /api/me and forgets everything', async () => {
		const r = signedIn();
		await r.store.init();
		await r.store.loadStats();
		expect(await r.store.deleteAccount()).toEqual({ ok: true });
		const calls = r.server.callsTo('DELETE /api/me');
		expect(calls).toHaveLength(1);
		expect(calls[0].init.credentials).toBe('same-origin');
		expect([r.store.loggedIn, r.store.user, r.store.stats]).toEqual([false, null, null]);
		await edit(r, 'Fantasma');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
	});

	it('a failure changes nothing and reports the code', async () => {
		const r = signedIn();
		await r.store.init();
		r.server.on('DELETE /api/me', json({ error: 'server_busy' }, 503));
		expect(await r.store.deleteAccount()).toEqual({ ok: false, code: 'server_busy' });
		expect(r.store.loggedIn).toBe(true);
		expect(r.store.user).not.toBeNull();
	});

	it('401 (session already gone) leaves a guest', async () => {
		const r = signedIn();
		await r.store.init();
		r.server.on('DELETE /api/me', json({ error: 'unauthorized' }, 401));
		expect(await r.store.deleteAccount()).toEqual({ ok: false, code: 'unauthorized' });
		expect(r.store.loggedIn).toBe(false);
	});
});

describe('loadStats', () => {
	it('GETs /api/me/stats and exposes the numbers', async () => {
		const r = signedIn();
		await r.store.init();
		expect(r.store.statsState).toBe('idle');
		const pending = r.store.loadStats();
		expect(r.store.statsState).toBe('loading');
		expect(await pending).toEqual({ ok: true });
		expect(r.store.stats).toEqual(STATS);
		expect(r.store.statsState).toBe('ready');
		expect(r.server.callsTo('GET /api/me/stats')[0].init).toEqual({ credentials: 'same-origin' });
	});

	it('as a guest it does not even ask', async () => {
		const r = rig();
		r.server.on('GET /api/session', json(SESSION_GUEST));
		await r.store.init();
		expect(await r.store.loadStats()).toEqual({ ok: false, code: 'unauthorized' });
		expect(r.server.keys).toEqual(['GET /api/session']);
	});

	it('an error is a state with its code, and a retry recovers', async () => {
		const r = signedIn();
		await r.store.init();
		r.server.on('GET /api/me/stats', json({ error: 'server_busy' }, 503));
		expect(await r.store.loadStats()).toEqual({ ok: false, code: 'server_busy' });
		expect([r.store.statsState, r.store.statsError, r.store.stats]).toEqual([
			'error',
			'server_busy',
			null
		]);
		r.server.on('GET /api/me/stats', json(STATS));
		await r.store.loadStats();
		expect([r.store.statsState, r.store.statsError]).toEqual(['ready', null]);
	});

	it('refreshing keeps the previous numbers on screen (no skeleton flash)', async () => {
		const r = signedIn();
		await r.store.init();
		await r.store.loadStats();
		const gate = deferred<Response>();
		r.server.on('GET /api/me/stats', () => gate.promise);
		const refresh = r.store.loadStats();
		expect([r.store.statsState, r.store.stats]).toEqual(['ready', STATS]);
		gate.resolve(json({ ...STATS, gamesPlayed: 13 }));
		await refresh;
		expect(r.store.stats?.gamesPlayed).toBe(13);
	});

	it('401 means the session is gone: guest again, no error state', async () => {
		const r = signedIn();
		await r.store.init();
		r.server.on('GET /api/me/stats', json({ error: 'unauthorized' }, 401));
		expect(await r.store.loadStats()).toEqual({ ok: false, code: 'unauthorized' });
		expect([r.store.loggedIn, r.store.statsState]).toEqual([false, 'idle']);
	});

	it('an answer that arrives after sign-out is ignored', async () => {
		const r = signedIn();
		await r.store.init();
		const gate = deferred<Response>();
		r.server.on('GET /api/me/stats', () => gate.promise);
		const pending = r.store.loadStats();
		await r.store.logout();
		gate.resolve(json(STATS));
		await pending;
		expect([r.store.stats, r.store.statsState]).toEqual([null, 'idle']);
	});

	it('only the latest of two overlapping loads counts', async () => {
		const r = signedIn();
		await r.store.init();
		const slow = deferred<Response>();
		r.server.on('GET /api/me/stats', () => slow.promise);
		const first = r.store.loadStats();
		r.server.on('GET /api/me/stats', json({ ...STATS, gamesPlayed: 99 }));
		await r.store.loadStats();
		slow.resolve(json({ ...STATS, gamesPlayed: 1 }));
		await first;
		expect(r.store.stats?.gamesPlayed).toBe(99);
	});
});

describe('terms', () => {
	const PENDING = { ...SESSION_IN, terms: TERMS_PENDING };

	it('the session feeds the shared terms state, for guests and for signed-in users', async () => {
		const guest = rig();
		guest.server.on('GET /api/session', json({ ...SESSION_GUEST, terms: TERMS_PENDING }));
		await guest.store.init();
		expect([
			guest.terms.status,
			guest.terms.acceptanceRequired,
			guest.terms.requiredVersion
		]).toEqual(['ready', true, '2026-10']);
		const accepted = signedIn();
		await accepted.store.init();
		expect(accepted.terms.acceptanceRequired).toBe(false);
		expect(accepted.terms.acceptedVersion).toBe('2026-10');
	});

	it('uploads wait for the acceptance: nothing is sent, no failure counted, nobody is nagged', async () => {
		const r = signedIn();
		r.server.on('GET /api/session', json(PENDING));
		await r.store.init();
		await tick();
		await edit(r, 'Nuevo');
		await vi.advanceTimersByTimeAsync(10_000);
		r.win.fire('pagehide');
		await vi.advanceTimersByTimeAsync(10_000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		expect(r.notify).not.toHaveBeenCalled();
		expect(await r.store.saveProfile()).toEqual({ ok: false, code: 'terms_required' });
	});

	it('resumes once the terms are accepted: the pending change is uploaded, once', async () => {
		const r = signedIn();
		r.server.on('GET /api/session', json(PENDING));
		await r.store.init();
		await tick();
		await edit(r, 'Nuevo');
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		r.terms.apply(TERMS_ACCEPTED);
		await tick();
		await vi.advanceTimersByTimeAsync(0);
		const puts = r.server.callsTo(PUT);
		expect(puts).toHaveLength(1);
		expect(puts[0].body).toEqual({
			nickname: 'Nuevo',
			avatar: { emoji: '🍓', color: 'strawberry' }
		});
		await vi.advanceTimersByTimeAsync(10_000);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
	});

	it('first login with the terms pending: the local nickname is uploaded after the acceptance', async () => {
		const r = signedIn(ME_FIRST, { nickname: 'Beto', avatar: { emoji: '🍉', color: 'lime' } });
		r.server.on('GET /api/session', json(PENDING));
		await r.store.init();
		await tick();
		await vi.advanceTimersByTimeAsync(5000);
		expect(r.server.callsTo(PUT)).toHaveLength(0);
		r.terms.apply(TERMS_ACCEPTED);
		await tick();
		await vi.advanceTimersByTimeAsync(0);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.server.callsTo(PUT)[0].body).toMatchObject({ nickname: 'Beto' });
	});

	it('403 terms_required from the server (a stale idea): pauses, never loops, and resumes after the acceptance', async () => {
		const r = await (async () => {
			const rg = signedIn();
			await rg.store.init();
			await tick();
			return rg;
		})();
		r.server.on(PUT, json({ error: 'terms_required' }, 403));
		await edit(r, 'Uno');
		await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.terms.acceptanceRequired).toBe(true);
		// no retry timer, no toast, even after many "failures" worth of time
		await vi.advanceTimersByTimeAsync(60_000);
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		for (let i = 0; i < SAVE_FAILURES_BEFORE_TOAST + 1; i++) {
			await edit(r, `Uno ${i}`);
			await vi.advanceTimersByTimeAsync(PROFILE_SAVE_DEBOUNCE_MS);
		}
		expect(r.server.callsTo(PUT)).toHaveLength(1);
		expect(r.notify).not.toHaveBeenCalled();
		r.server.on(PUT, empty());
		r.terms.apply(TERMS_ACCEPTED);
		await tick();
		await vi.advanceTimersByTimeAsync(0);
		expect(r.server.callsTo(PUT)).toHaveLength(2);
		expect(r.server.callsTo(PUT)[1].body).toMatchObject({
			nickname: `Uno ${SAVE_FAILURES_BEFORE_TOAST}`
		});
	});

	it('logging out and deleting the account never wait for the terms', async () => {
		const r = signedIn();
		r.server.on('GET /api/session', json(PENDING));
		await r.store.init();
		await tick();
		await edit(r, 'Pendiente');
		expect(await r.store.logout()).toEqual({ ok: true });
		expect(r.server.callsTo('POST /api/auth/logout')).toHaveLength(1);
		const d = signedIn();
		d.server.on('GET /api/session', json(PENDING));
		await d.store.init();
		expect(await d.store.deleteAccount()).toEqual({ ok: true });
	});
});
