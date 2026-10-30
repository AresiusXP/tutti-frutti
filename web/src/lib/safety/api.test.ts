// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { ApiError } from '$lib/api';
import { FakeServer, empty, json } from '$lib/account/account.testkit';
import { createSafetyApi, parseTerms } from './api';
import { TERMS_DUE, TERMS_OK } from './safety.testkit';

function setup() {
	const server = new FakeServer();
	return { server, api: createSafetyApi(server.fetch) };
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
	it('POST /api/terms/accept with a JSON body and same-origin credentials', async () => {
		const { server, api } = setup();
		server.on('POST /api/terms/accept', json(TERMS_OK));
		const req = { version: '2026-10', locale: 'es', age13Confirmed: true };
		expect(await api.acceptTerms(req)).toEqual(TERMS_OK);
		expect(server.fetch).toHaveBeenCalledWith('/api/terms/accept', {
			credentials: 'same-origin',
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(req)
		});
	});

	it('POST /api/reports sends exactly the report and reads the id of a 201 or a 200', async () => {
		const { server, api } = setup();
		const req = {
			kind: 'player',
			code: 'KT7P',
			ref: { playerId: 'p2' },
			reason: 'spam',
			idempotencyKey: 'key-abcdefgh'
		};
		server.on('POST /api/reports', json({ id: 'r1' }, 201));
		expect(await api.report(req)).toEqual({ id: 'r1' });
		server.on('POST /api/reports', json({ id: 'r1' }, 200));
		expect(await api.report(req)).toEqual({ id: 'r1' });
		expect(server.callsTo('POST /api/reports')[0].body).toEqual(req);
	});

	it('blocks: GET, POST and DELETE /api/blocks/{playerId} (encoded)', async () => {
		const { server, api } = setup();
		server
			.on('GET /api/blocks', json({ blocks: [{ playerId: 'p2', name: 'Beto', blockedAt: 5 }] }))
			.on(
				'POST /api/blocks',
				json({ block: { playerId: 'p2', name: 'Beto', blockedAt: 5 }, leftRooms: ['KT7P'] })
			)
			.on('DELETE /api/blocks/bot%3Ax', empty());
		expect(await api.blocks()).toEqual([{ playerId: 'p2', name: 'Beto', blockedAt: 5 }]);
		expect(await api.block({ playerId: 'p2', name: 'Beto' })).toEqual({
			block: { playerId: 'p2', name: 'Beto', blockedAt: 5 },
			leftRooms: ['KT7P']
		});
		await api.unblock('bot:x');
		expect(server.callsTo('POST /api/blocks')[0].body).toEqual({ playerId: 'p2', name: 'Beto' });
		expect(server.callsTo('DELETE /api/blocks/bot%3Ax')).toHaveLength(1);
	});
});

describe('errors', () => {
	it.each([
		['POST /api/reports', 429, 'rate_limited'],
		['POST /api/reports', 503, 'server_busy'],
		['POST /api/reports', 403, 'not_in_room'],
		['POST /api/reports', 400, 'invalid_report'],
		['POST /api/terms/accept', 409, 'terms_version_mismatch'],
		['POST /api/terms/accept', 400, 'age_confirmation_required'],
		['POST /api/blocks', 409, 'block_limit'],
		['POST /api/blocks', 400, 'invalid_block']
	])('%s %s -> %s with the status and Retry-After', async (route, status, code) => {
		const { server, api } = setup();
		server.on(route, json({ error: code }, status, { 'Retry-After': '7' }));
		const call =
			route === 'POST /api/reports'
				? api.report({ kind: 'player', ref: {}, reason: 'x', idempotencyKey: 'k' })
				: route === 'POST /api/blocks'
					? api.block({ playerId: 'p' })
					: api.acceptTerms({ version: 'v', locale: 'es', age13Confirmed: true });
		const err = await rejection(call);
		expect([err.status, err.code]).toEqual([
			status,
			status === 429 ? 'rate_limited' : status === 503 ? 'server_busy' : code
		]);
		if (status === 429 || status === 503) expect(err.retryAfter).toBe(7);
	});

	it('no response is status 0 "network"; a body that is not JSON is "unknown"', async () => {
		const { server, api } = setup();
		server.on('POST /api/reports', new Error('offline'));
		const offline = await rejection(
			api.report({ kind: 'player', ref: {}, reason: 'x', idempotencyKey: 'k' })
		);
		expect([offline.status, offline.code, offline.retryable]).toEqual([0, 'network', true]);
		server.on('POST /api/reports', new Response('<html>', { status: 200 }));
		expect(
			(await rejection(api.report({ kind: 'player', ref: {}, reason: 'x', idempotencyKey: 'k' })))
				.code
		).toBe('unknown');
	});

	it('a success body that is not what the contract says is "unknown", never a fake success', async () => {
		const { server, api } = setup();
		server.on('POST /api/reports', json({ ok: true }, 201));
		await expect(
			api.report({ kind: 'player', ref: {}, reason: 'x', idempotencyKey: 'k' })
		).rejects.toThrow();
		server.on('GET /api/blocks', json({ nope: 1 }));
		await expect(api.blocks()).rejects.toThrow();
		server.on('POST /api/terms/accept', json({ requiredVersion: 1 }));
		await expect(
			api.acceptTerms({ version: 'v', locale: 'es', age13Confirmed: true })
		).rejects.toThrow();
	});

	it('drops malformed block entries instead of failing the whole list', async () => {
		const { server, api } = setup();
		server.on(
			'GET /api/blocks',
			json({
				blocks: [
					null,
					{ playerId: '' },
					{ playerId: 'p2' },
					{ playerId: 'p3', name: 'C', blockedAt: 3 }
				]
			})
		);
		expect(await api.blocks()).toEqual([
			{ playerId: 'p2', name: '', blockedAt: 0 },
			{ playerId: 'p3', name: 'C', blockedAt: 3 }
		]);
	});
});

describe('parseTerms', () => {
	it('accepts the two shapes of the server and refuses the rest', () => {
		expect(parseTerms(TERMS_DUE)).toEqual(TERMS_DUE);
		expect(parseTerms(TERMS_OK)).toEqual(TERMS_OK);
		for (const bad of [
			null,
			1,
			[],
			{},
			{ requiredVersion: 'v' },
			{ ...TERMS_OK, acceptedVersion: 1 }
		]) {
			expect(parseTerms(bad)).toBeNull();
		}
	});
});
