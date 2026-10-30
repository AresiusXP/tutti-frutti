// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { BlocksStore } from './blocks.svelte';
import { apiError, deferred, FakeSafetyApi } from './safety.testkit';

const ana = { playerId: 'p1', name: 'Ana', blockedAt: 10 };
const beto = { playerId: 'p2', name: 'Beto', blockedAt: 20 };

function setup(list = [beto, ana]) {
	const api = new FakeSafetyApi();
	api.list = list;
	return { api, store: new BlocksStore(api) };
}

describe('BlocksStore', () => {
	it('loads the list (newest first as the server sends it) and remembers it', async () => {
		const { store, api } = setup();
		expect(store.status).toBe('idle');
		expect(await store.load()).toBe(true);
		expect(store.status).toBe('ready');
		expect(store.list.map((b) => b.playerId)).toEqual(['p2', 'p1']);
		expect(store.isBlocked('p1')).toBe(true);
		expect(store.isBlocked('p9')).toBe(false);
		expect(api.listCalls).toBe(1);
	});

	it('shows loading only while it has nothing to show', async () => {
		const { store, api } = setup();
		const pending = deferred<(typeof ana)[]>();
		api.listAnswer = pending.promise;
		const first = store.load();
		expect(store.status).toBe('loading');
		pending.resolve([ana]);
		await first;
		api.listAnswer = new Promise(() => undefined);
		void store.load();
		expect(store.status).toBe('ready');
	});

	it('a failed refresh keeps the cached list; with no list it is an error with a code', async () => {
		const { store, api } = setup();
		api.listAnswer = apiError(0, 'network');
		expect(await store.load()).toBe(false);
		expect([store.status, store.error]).toEqual(['error', 'network']);
		api.listAnswer = null;
		await store.load();
		api.listAnswer = apiError(503, 'server_busy');
		await store.load();
		expect(store.status).toBe('ready');
		expect(store.error).toBe('server_busy');
		expect(store.list).toHaveLength(2);
	});

	it('a late answer of an older load never overwrites a newer change', async () => {
		const { store, api } = setup([]);
		const slow = deferred<(typeof ana)[]>();
		api.listAnswer = slow.promise;
		const stale = store.load();
		api.blockAnswer = { block: ana, leftRooms: [] };
		await store.block({ id: 'p1', name: 'Ana' });
		slow.resolve([]);
		await stale;
		expect(store.list.map((b) => b.playerId)).toEqual(['p1']);
	});

	it('block() remembers the new entry first and returns the rooms the server removed us from', async () => {
		const { store, api } = setup([ana]);
		await store.load();
		api.blockAnswer = { block: beto, leftRooms: ['KT7P'] };
		expect(await store.block({ id: 'p2', name: 'Beto' })).toEqual({
			ok: true,
			leftRooms: ['KT7P']
		});
		expect(api.blocked).toEqual([{ playerId: 'p2', name: 'Beto' }]);
		expect(store.list.map((b) => b.playerId)).toEqual(['p2', 'p1']);
		// blocking again replaces, never duplicates
		await store.block({ id: 'p2', name: 'Beto' });
		expect(store.list.filter((b) => b.playerId === 'p2')).toHaveLength(1);
	});

	it.each([
		[400, 'invalid_block'],
		[409, 'block_limit'],
		[0, 'network']
	])('block() %s: ok false with the code, nothing remembered', async (status, code) => {
		const { store, api } = setup([]);
		api.blockAnswer = apiError(status, code);
		expect(await store.block({ id: 'p2', name: 'Beto' })).toEqual({ ok: false, code });
		expect(store.list).toEqual([]);
	});

	it('unblock() leaves the list only after the server said so', async () => {
		const { store, api } = setup();
		await store.load();
		api.unblockAnswer = apiError(503, 'server_busy');
		expect(await store.unblock('p1')).toEqual({ ok: false, code: 'server_busy' });
		expect(store.isBlocked('p1')).toBe(true);
		api.unblockAnswer = undefined;
		expect(await store.unblock('p1')).toEqual({ ok: true });
		expect(api.unblocked).toEqual(['p1', 'p1']);
		expect(store.isBlocked('p1')).toBe(false);
	});

	it('expectRemoval marks the room the server is about to take the player out of', () => {
		const { store } = setup();
		store.expectRemoval('KT7P');
		expect(store.expecting).toBe('KT7P');
		store.expectRemoval(null);
		expect(store.expecting).toBeNull();
	});
});
