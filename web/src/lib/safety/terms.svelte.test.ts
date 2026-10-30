// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { DECLINED, TermsStore } from './terms.svelte';
import { apiError, deferred, FakeSafetyApi, TERMS_DUE, TERMS_OK } from './safety.testkit';

function setup(opts: { session?: () => Promise<{ terms: typeof TERMS_OK }> } = {}) {
	const api = new FakeSafetyApi();
	const loadSession = vi.fn(opts.session ?? (() => Promise.resolve({ terms: TERMS_DUE })));
	const store = new TermsStore({ api, loadSession });
	return { api, loadSession, store };
}

beforeEach(() => i18n.set('es'));

describe('state', () => {
	it('starts unknown and takes what the session said', () => {
		const { store } = setup();
		expect(store.status).toBe('unknown');
		expect(store.blocksUploads).toBe(false);
		store.apply(TERMS_DUE);
		expect([store.status, store.requiredVersion, store.acceptedVersion]).toEqual([
			'ready',
			'2026-10',
			null
		]);
		expect(store.blocksUploads).toBe(true);
		store.apply(TERMS_OK);
		expect(store.blocksUploads).toBe(false);
	});

	it('require() (the server said terms_required) turns a stale "accepted" into "pending"', () => {
		const { store } = setup();
		store.apply(TERMS_OK);
		store.require();
		expect(store.acceptanceRequired).toBe(true);
		expect(store.blocksUploads).toBe(true);
	});
});

describe('ensure', () => {
	it('resolves true at once when the terms are accepted, without opening the gate', async () => {
		const { store, loadSession } = setup();
		store.apply(TERMS_OK);
		expect(await store.ensure()).toBe(true);
		expect(store.open).toBe(false);
		expect(loadSession).not.toHaveBeenCalled();
	});

	it('asks the server once when the state is unknown, then opens the gate when they are due', async () => {
		const { store, loadSession } = setup();
		const waiting = store.ensure();
		await vi.waitFor(() => expect(store.open).toBe(true));
		expect(loadSession).toHaveBeenCalledTimes(1);
		store.apply(TERMS_OK);
		expect(await waiting).toBe(true);
		expect(store.open).toBe(false);
	});

	it('lets the intent go ahead when the state cannot be loaded (the server decides)', async () => {
		const { store } = setup({ session: () => Promise.reject(apiError(0, 'network')) });
		expect(await store.ensure()).toBe(true);
		expect(store.open).toBe(false);
		expect(store.status).toBe('unknown');
	});

	it('shares one session request between concurrent callers', async () => {
		const gate = deferred<{ terms: typeof TERMS_OK }>();
		const { store, loadSession } = setup({ session: () => gate.promise });
		const a = store.ensure();
		const b = store.ensure();
		gate.resolve({ terms: TERMS_OK });
		expect(await Promise.all([a, b])).toEqual([true, true]);
		expect(loadSession).toHaveBeenCalledTimes(1);
	});

	it('every waiter of an open gate gets the same answer', async () => {
		const { store } = setup();
		store.apply(TERMS_DUE);
		const a = store.ensure();
		const b = store.ensure();
		store.dismiss();
		expect(await Promise.all([a, b])).toEqual([false, false]);
		expect(store.open).toBe(false);
	});
});

describe('accept', () => {
	it('sends the required version, the screen language and the age confirmation, then continues the intent', async () => {
		const { store, api } = setup();
		store.apply(TERMS_DUE);
		i18n.set('nl');
		const intent = store.ensure();
		await store.accept();
		expect(api.accepts).toEqual([{ version: '2026-10', locale: 'nl', age13Confirmed: true }]);
		expect(await intent).toBe(true);
		expect(store.acceptanceRequired).toBe(false);
		expect(store.acceptedVersion).toBe('2026-10');
		expect(store.open).toBe(false);
		expect(store.saving).toBe(false);
	});

	it('does nothing while the gate is closed or a request is in flight', async () => {
		const { store, api } = setup();
		store.apply(TERMS_DUE);
		await store.accept();
		expect(api.accepts).toHaveLength(0);
		const pending = deferred<typeof TERMS_OK>();
		api.acceptAnswers = [pending.promise];
		void store.ensure();
		const first = store.accept();
		await store.accept();
		expect(api.accepts).toHaveLength(1);
		expect(store.saving).toBe(true);
		pending.resolve(TERMS_OK);
		await first;
	});

	it('on a network error keeps the gate open with the error and lets the player try again', async () => {
		const { store, api } = setup();
		store.apply(TERMS_DUE);
		const intent = store.ensure();
		api.acceptAnswers = [apiError(0, 'network'), TERMS_OK];
		await store.accept();
		expect(store.error).toBe('network');
		expect(store.open).toBe(true);
		await store.accept();
		expect(store.error).toBeNull();
		expect(await intent).toBe(true);
		expect(api.accepts).toHaveLength(2);
	});

	it.each(['server_busy', 'rate_limited'])('shows %s as a retryable error', async (code) => {
		const { store, api } = setup();
		store.apply(TERMS_DUE);
		void store.ensure();
		api.acceptAnswers = [apiError(code === 'rate_limited' ? 429 : 503, code)];
		await store.accept();
		expect(store.error).toBe(code);
		expect(store.open).toBe(true);
	});

	it('terms_version_mismatch: refetches the session and shows the gate again with the new version', async () => {
		const { store, api, loadSession } = setup({
			session: () => Promise.resolve({ terms: { ...TERMS_DUE, requiredVersion: '2026-11' } })
		});
		store.apply(TERMS_DUE);
		const intent = store.ensure();
		api.acceptAnswers = [
			apiError(409, 'terms_version_mismatch'),
			{ ...TERMS_OK, requiredVersion: '2026-11', acceptedVersion: '2026-11' }
		];
		await store.accept();
		expect(loadSession).toHaveBeenCalledTimes(1);
		expect(store.open).toBe(true);
		expect(store.updated).toBe(true);
		expect(store.requiredVersion).toBe('2026-11');
		expect(store.error).toBeNull();
		await store.accept();
		expect(api.accepts.map((a) => a.version)).toEqual(['2026-10', '2026-11']);
		expect(await intent).toBe(true);
		expect(store.updated).toBe(false);
	});

	it('terms_version_mismatch with no way to refetch ends as a network error', async () => {
		const { store, api } = setup({ session: () => Promise.reject(apiError(0, 'network')) });
		store.apply(TERMS_DUE);
		void store.ensure();
		api.acceptAnswers = [apiError(409, 'terms_version_mismatch')];
		await store.accept();
		expect(store.error).toBe('network');
		expect(store.open).toBe(true);
	});

	it('401 (no guest cookie any more): mints one with the session call and asks again once', async () => {
		const { store, api, loadSession } = setup();
		store.apply(TERMS_DUE);
		const intent = store.ensure();
		api.acceptAnswers = [apiError(401, 'unauthorized'), TERMS_OK];
		await store.accept();
		expect(loadSession).toHaveBeenCalledTimes(1);
		expect(api.accepts).toHaveLength(2);
		expect(await intent).toBe(true);
	});

	it('dismiss while the gate is open declines for everybody and forgets the error', async () => {
		const { store, api } = setup();
		store.apply(TERMS_DUE);
		const intent = store.ensure();
		api.acceptAnswers = [apiError(0, 'network')];
		await store.accept();
		store.dismiss();
		expect(await intent).toBe(false);
		expect(store.error).toBeNull();
		expect(store.open).toBe(false);
		expect(store.acceptanceRequired).toBe(true);
	});
});

describe('run', () => {
	it('runs the action when the terms are accepted', async () => {
		const { store } = setup();
		store.apply(TERMS_OK);
		const action = vi.fn(() => Promise.resolve('room'));
		expect(await store.run(action)).toBe('room');
		expect(action).toHaveBeenCalledTimes(1);
	});

	it('is DECLINED, without running anything, when the gate is closed', async () => {
		const { store } = setup();
		store.apply(TERMS_DUE);
		const action = vi.fn(() => Promise.resolve('room'));
		const result = store.run(action);
		await vi.waitFor(() => expect(store.open).toBe(true));
		store.dismiss();
		expect(await result).toBe(DECLINED);
		expect(action).not.toHaveBeenCalled();
	});

	it('terms_required from the server (a stale client): gate, then the action ONCE more', async () => {
		const { store, api } = setup();
		store.apply(TERMS_OK);
		const action = vi
			.fn<() => Promise<string>>()
			.mockRejectedValueOnce(apiError(403, 'terms_required'))
			.mockResolvedValueOnce('room');
		const result = store.run(action);
		await vi.waitFor(() => expect(store.open).toBe(true));
		expect(store.acceptanceRequired).toBe(true);
		await store.accept();
		expect(await result).toBe('room');
		expect(action).toHaveBeenCalledTimes(2);
		expect(api.accepts).toHaveLength(1);
	});

	it('a second terms_required after the acceptance reaches the caller (never a loop)', async () => {
		const { store } = setup();
		store.apply(TERMS_OK);
		const refusal = apiError(403, 'terms_required');
		const action = vi.fn<() => Promise<string>>().mockRejectedValue(refusal);
		const result = store.run(action);
		const failed = expect(result).rejects.toBe(refusal);
		await vi.waitFor(() => expect(store.open).toBe(true));
		await store.accept();
		await failed;
		expect(action).toHaveBeenCalledTimes(2);
	});

	it('any other error goes straight to the caller', async () => {
		const { store } = setup();
		store.apply(TERMS_OK);
		const boom = apiError(403, 'join_unavailable');
		await expect(store.run(() => Promise.reject(boom))).rejects.toBe(boom);
		expect(store.open).toBe(false);
	});
});
