import { describe, expect, it, vi } from 'vitest';
import { ApiError, createApi } from './api';

function api(handler: (url: string, init?: RequestInit) => Response | Promise<Response> | Error) {
	const fetchMock = vi.fn((url: string, init?: RequestInit) => {
		const reply = handler(url, init);
		return reply instanceof Error ? Promise.reject(reply) : Promise.resolve(reply);
	});
	return { api: createApi(fetchMock), fetchMock };
}

const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json', ...headers }
	});

const fail = async (promise: Promise<unknown>): Promise<ApiError> => {
	try {
		await promise;
	} catch (e) {
		expect(e).toBeInstanceOf(ApiError);
		return e as ApiError;
	}
	throw new Error('expected a rejection');
};

describe('api.ensureSession', () => {
	it('GETs /api/session with same-origin credentials', async () => {
		const { api: a, fetchMock } = api(() => json({ guest: true }));
		await a.ensureSession();
		expect(fetchMock).toHaveBeenCalledWith('/api/session', {
			credentials: 'same-origin',
			signal: undefined
		});
	});

	it('releases the response body instead of leaving it to be aborted later', async () => {
		const res = json({ guest: true });
		const cancel = vi.spyOn(res.body as ReadableStream, 'cancel');
		const { api: a } = api(() => res);
		await a.ensureSession();
		expect(cancel).toHaveBeenCalledOnce();
	});

	it('still succeeds when the body cannot be cancelled or is missing', async () => {
		const locked = json({ guest: true });
		locked.body?.getReader(); // a locked stream rejects cancel()
		await expect(api(() => locked).api.ensureSession()).resolves.toBeUndefined();
		await expect(
			api(() => new Response(null, { status: 204 })).api.ensureSession()
		).resolves.toBeUndefined();
	});
});

describe('api.getRoom', () => {
	const info = {
		code: 'KT7P',
		phase: 'lobby',
		players: 2,
		maxPlayers: 10,
		lang: 'es',
		joinable: true
	};

	it('returns the room info', async () => {
		const { api: a, fetchMock } = api(() => json(info));
		await expect(a.getRoom('KT7P')).resolves.toEqual(info);
		expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms/KT7P');
	});

	it('404 is room_not_found whatever the body says', async () => {
		for (const body of [{ error: 'room_not_found' }, { error: 'not_found' }]) {
			const e = await fail(api(() => json(body, 404)).api.getRoom('KT7P'));
			expect(e).toMatchObject({ status: 404, code: 'room_not_found', retryable: false });
		}
	});

	it('503 from Go or from the proxy is server_busy and retryable, with Retry-After', async () => {
		const goBusy = await fail(
			api(() => json({ error: 'server_busy' }, 503, { 'Retry-After': '2' })).api.getRoom('KT7P')
		);
		expect(goBusy).toMatchObject({
			status: 503,
			code: 'server_busy',
			retryAfter: 2,
			retryable: true
		});
		const traefik = await fail(
			api(() => new Response('no available server', { status: 503 })).api.getRoom('KT7P')
		);
		expect(traefik).toMatchObject({ status: 503, code: 'server_busy', retryAfter: null });
		for (const status of [502, 504]) {
			const e = await fail(api(() => new Response('', { status })).api.getRoom('KT7P'));
			expect(e.code).toBe('server_busy');
		}
	});

	it('429 is rate_limited', async () => {
		const e = await fail(
			api(() => json({ error: 'rate_limited' }, 429, { 'Retry-After': '6' })).api.getRoom('KT7P')
		);
		expect(e).toMatchObject({ status: 429, code: 'rate_limited', retryAfter: 6, retryable: true });
	});

	it('a network failure is code network, status 0', async () => {
		const e = await fail(api(() => new TypeError('Failed to fetch')).api.getRoom('KT7P'));
		expect(e).toMatchObject({ status: 0, code: 'network', retryable: true });
	});

	it('an OK answer that is not JSON is an unknown error, not a crash', async () => {
		const e = await fail(api(() => new Response('<html>', { status: 200 })).api.getRoom('KT7P'));
		expect(e).toMatchObject({ status: 200, code: 'unknown' });
	});

	it('rethrows aborts untouched', async () => {
		const abort = new AbortController();
		abort.abort();
		const { api: a } = api(() => new DOMException('aborted', 'AbortError') as unknown as Error);
		await expect(a.getRoom('KT7P', { signal: abort.signal })).rejects.not.toBeInstanceOf(ApiError);
	});
});

describe('api.createRoom', () => {
	const req = { name: 'Ana', avatar: { emoji: '🍌', color: 'banana' }, lang: 'es' as const };

	it('POSTs JSON with same-origin credentials and returns the code', async () => {
		const { api: a, fetchMock } = api(() => json({ code: 'KT7P' }, 201));
		await expect(a.createRoom(req)).resolves.toEqual({ code: 'KT7P' });
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/rooms');
		expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin' });
		expect((init!.headers as Record<string, string>)['Content-Type']).toBe('application/json');
		expect(JSON.parse(init!.body as string)).toEqual(req);
	});

	it.each([
		[400, 'invalid_name'],
		[400, 'invalid_avatar'],
		[400, 'invalid_lang'],
		[403, 'forbidden_origin'],
		[500, 'internal']
	])('maps %i {error: %s} to the code', async (status, code) => {
		const e = await fail(api(() => json({ error: code }, status)).api.createRoom(req));
		expect(e).toMatchObject({ status, code });
	});

	it('503 server_busy and 429 rate_limited', async () => {
		expect(
			(await fail(api(() => json({ error: 'server_busy' }, 503)).api.createRoom(req))).code
		).toBe('server_busy');
		expect((await fail(api(() => json({}, 429)).api.createRoom(req))).code).toBe('rate_limited');
	});

	it('an unexplained failure is unknown', async () => {
		const e = await fail(api(() => new Response('boom', { status: 418 })).api.createRoom(req));
		expect(e).toMatchObject({ status: 418, code: 'unknown', retryable: false });
	});

	it('the default client uses the global fetch lazily', async () => {
		const original = globalThis.fetch;
		globalThis.fetch = vi.fn(() => Promise.resolve(json({ guest: true }))) as typeof fetch;
		try {
			const { api: shared } = await import('./api');
			await shared.ensureSession();
			expect(globalThis.fetch).toHaveBeenCalledOnce();
		} finally {
			globalThis.fetch = original;
		}
	});
});
