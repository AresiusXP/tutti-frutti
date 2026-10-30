import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '$lib/api';
import { createAccountApi } from './api';
import {
	ME,
	ME_FIRST,
	STATS,
	TERMS_ACCEPTED,
	TERMS_PENDING,
	FakeServer,
	empty,
	json
} from './account.testkit';

function setup() {
	const server = new FakeServer();
	return { server, api: createAccountApi(server.fetch) };
}

const rejection = async (promise: Promise<unknown>): Promise<ApiError> => {
	try {
		await promise;
	} catch (e) {
		expect(e).toBeInstanceOf(ApiError);
		return e as ApiError;
	}
	throw new Error('expected a rejection');
};

describe('request shapes', () => {
	it('GET /api/session with same-origin credentials and nothing else', async () => {
		const { server, api } = setup();
		server.on('GET /api/session', json({ guest: true, googleEnabled: true, loggedIn: false }));
		await api.session();
		expect(server.fetch).toHaveBeenCalledWith('/api/session', { credentials: 'same-origin' });
	});

	it('GET /api/me and /api/me/stats', async () => {
		const { server, api } = setup();
		server.on('GET /api/me', json(ME)).on('GET /api/me/stats', json(STATS));
		await api.me();
		await api.stats();
		expect(server.fetch).toHaveBeenNthCalledWith(1, '/api/me', { credentials: 'same-origin' });
		expect(server.fetch).toHaveBeenNthCalledWith(2, '/api/me/stats', {
			credentials: 'same-origin'
		});
	});

	it('PUT /api/me/profile sends exactly nickname + avatar as JSON', async () => {
		const { server, api } = setup();
		server.on('PUT /api/me/profile', empty());
		await api.saveProfile({ nickname: 'Ana', avatar: { emoji: '🍓', color: 'strawberry' } });
		expect(server.fetch).toHaveBeenCalledWith('/api/me/profile', {
			credentials: 'same-origin',
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: '{"nickname":"Ana","avatar":{"emoji":"🍓","color":"strawberry"}}'
		});
	});

	it('PUT /api/me/profile can outlive the page with keepalive', async () => {
		const { server, api } = setup();
		server.on('PUT /api/me/profile', empty());
		await api.saveProfile(
			{ nickname: 'Ana', avatar: { emoji: '🍓', color: 'strawberry' } },
			{ keepalive: true }
		);
		expect(server.calls[0].init.keepalive).toBe(true);
	});

	it('DELETE /api/me and POST /api/auth/logout take no body', async () => {
		const { server, api } = setup();
		server.on('DELETE /api/me', empty()).on('POST /api/auth/logout', empty());
		await api.deleteMe();
		await api.logout();
		expect(server.fetch).toHaveBeenNthCalledWith(1, '/api/me', {
			credentials: 'same-origin',
			method: 'DELETE'
		});
		expect(server.fetch).toHaveBeenNthCalledWith(2, '/api/auth/logout', {
			credentials: 'same-origin',
			method: 'POST'
		});
	});
});

describe('responses', () => {
	it('session: flags are strict booleans; a Phase 3 server (only `guest`) means no Google, not logged in, no terms', async () => {
		const { server, api } = setup();
		const none = { requiredVersion: '', acceptedVersion: null, acceptanceRequired: false };
		server.on('GET /api/session', json({ guest: true }));
		expect(await api.session()).toEqual({
			guest: true,
			googleEnabled: false,
			loggedIn: false,
			terms: none
		});
		server.on('GET /api/session', json({ guest: true, googleEnabled: 'yes', loggedIn: 1 }));
		expect(await api.session()).toEqual({
			guest: true,
			googleEnabled: false,
			loggedIn: false,
			terms: none
		});
		server.on(
			'GET /api/session',
			json({ guest: true, googleEnabled: true, loggedIn: true, terms: TERMS_ACCEPTED })
		);
		expect(await api.session()).toEqual({
			guest: true,
			googleEnabled: true,
			loggedIn: true,
			terms: TERMS_ACCEPTED
		});
	});

	it('session: carries the terms status of the guest and ignores a malformed one', async () => {
		const { server, api } = setup();
		server.on('GET /api/session', json({ guest: true, terms: TERMS_PENDING }));
		expect((await api.session()).terms).toEqual(TERMS_PENDING);
		for (const bad of [
			null,
			'x',
			{ requiredVersion: 1 },
			{ ...TERMS_PENDING, acceptedVersion: 3 }
		]) {
			server.on('GET /api/session', json({ guest: true, terms: bad }));
			expect((await api.session()).terms.acceptanceRequired, JSON.stringify(bad)).toBe(false);
		}
	});

	it('me: with and without a saved profile', async () => {
		const { server, api } = setup();
		server.on('GET /api/me', json(ME));
		expect(await api.me()).toEqual(ME);
		server.on('GET /api/me', json(ME_FIRST));
		expect(await api.me()).toEqual(ME_FIRST);
	});

	it.each([
		['not an object', 'hello'],
		['no id', { name: 'A', profile: null }],
		['numeric name', { id: 'u', name: 1, profile: null }],
		['profile missing', { id: 'u', name: 'A' }],
		['profile without avatar', { id: 'u', name: 'A', profile: { nickname: 'x' } }],
		[
			'avatar with a number',
			{ id: 'u', name: 'A', profile: { nickname: 'x', avatar: { emoji: 1, color: 'c' } } }
		]
	])('me: a malformed answer (%s) is an unknown error, not a crash', async (_, body) => {
		const { server, api } = setup();
		server.on('GET /api/me', json(body));
		expect((await rejection(api.me())).code).toBe('unknown');
	});

	it('stats: five non-negative integers', async () => {
		const { server, api } = setup();
		server.on('GET /api/me/stats', json({ ...STATS, extra: 1 }));
		expect(await api.stats()).toEqual(STATS);
	});

	it.each([
		['negative', { ...STATS, wins: -1 }],
		['fractional', { ...STATS, totalPoints: 1.5 }],
		['string', { ...STATS, bestRound: '80' }],
		['missing', { gamesPlayed: 1 }],
		['null', null]
	])('stats: a %s value is rejected', async (_, body) => {
		const { server, api } = setup();
		server.on('GET /api/me/stats', json(body));
		expect((await rejection(api.stats())).code).toBe('unknown');
	});

	it('a 200 that is not JSON (SPA fallback where the proxy is missing) is an unknown error', async () => {
		const { server, api } = setup();
		server.on('GET /api/session', new Response('<html></html>', { status: 200 }));
		const e = await rejection(api.session());
		expect(e.code).toBe('unknown');
		expect(e.status).toBe(200);
	});
});

describe('errors', () => {
	it('401 is `unauthorized` whatever the body says', async () => {
		const { server, api } = setup();
		server.on('GET /api/me', json({ error: 'nope' }, 401));
		const e = await rejection(api.me());
		expect([e.status, e.code]).toEqual([401, 'unauthorized']);
		server.on('GET /api/me/stats', new Response('Unauthorized', { status: 401 }));
		expect((await rejection(api.stats())).code).toBe('unauthorized');
	});

	it('uses the server error code for validation failures', async () => {
		const { server, api } = setup();
		server.on('PUT /api/me/profile', json({ error: 'invalid_name' }, 400));
		const e = await rejection(
			api.saveProfile({ nickname: '', avatar: { emoji: 'x', color: 'y' } })
		);
		expect([e.status, e.code]).toEqual([400, 'invalid_name']);
	});

	it('429 keeps Retry-After; 502/503/504 are server_busy even with an HTML body', async () => {
		const { server, api } = setup();
		server.on('GET /api/me', json({ error: 'rate_limited' }, 429, { 'Retry-After': '7' }));
		const limited = await rejection(api.me());
		expect([limited.code, limited.retryAfter, limited.retryable]).toEqual([
			'rate_limited',
			7,
			true
		]);
		for (const status of [502, 503, 504]) {
			server.on('GET /api/me', new Response('no available server', { status }));
			expect((await rejection(api.me())).code).toBe('server_busy');
		}
	});

	it('an unexpected status without a body is unknown', async () => {
		const { server, api } = setup();
		server.on('DELETE /api/me', new Response(null, { status: 418 }));
		expect((await rejection(api.deleteMe())).code).toBe('unknown');
	});

	it('no response at all is status 0 / network', async () => {
		const fetchFn = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')));
		const e = await rejection(createAccountApi(fetchFn).logout());
		expect([e.status, e.code]).toEqual([0, 'network']);
	});
});
