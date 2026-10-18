import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { catalog } from './catalog.svelte';
import { fixtureFetch, listingFor } from './fixtures';

describe('catalog store', () => {
	beforeEach(() => catalog.reset());
	afterEach(() => {
		catalog.reset();
		vi.useRealTimers();
	});

	it('loads a language once and caches it', async () => {
		const fetcher = fixtureFetch();
		catalog.reset(fetcher);
		expect(catalog.state('es')).toBeUndefined();
		const first = catalog.load('es');
		expect(catalog.state('es')).toEqual({ status: 'loading' });
		await first;
		expect(catalog.listing('es')).toEqual(listingFor('es'));
		await catalog.load('es');
		expect(fetcher.calls()).toBe(1);
		await catalog.load('en');
		expect(catalog.listing('en')?.categories[0].label).toBe('Name');
		expect(fetcher.calls()).toBe(2);
		expect(catalog.listing('es')?.categories[0].label).toBe('Nombre');
	});

	it('shares one request between concurrent callers', async () => {
		const fetcher = fixtureFetch({ delayMs: 1 });
		catalog.reset(fetcher);
		await Promise.all([catalog.load('es'), catalog.load('es')]);
		expect(fetcher.calls()).toBe(1);
	});

	it('requests the endpoint of the language', async () => {
		const fetcher = vi.fn(fixtureFetch());
		catalog.reset(fetcher as unknown as typeof fetch);
		await catalog.load('en');
		expect(fetcher.mock.calls[0][0]).toBe('/api/categories?lang=en');
	});

	it('reports an error and retries', async () => {
		const fetcher = fixtureFetch({ failFirst: 1 });
		catalog.reset(fetcher);
		await catalog.load('es');
		expect(catalog.state('es')).toEqual({ status: 'error' });
		expect(catalog.listing('es')).toBeNull();
		await catalog.load('es');
		expect(catalog.state('es')?.status).toBe('ready');
	});

	it('rejects a malformed body and a network failure', async () => {
		catalog.reset((() => Promise.resolve(new Response('{"nope":1}'))) as typeof fetch);
		await catalog.load('es');
		expect(catalog.state('es')?.status).toBe('error');
		catalog.reset((() => Promise.resolve(new Response('null'))) as typeof fetch);
		await catalog.load('es');
		expect(catalog.state('es')?.status).toBe('error');
		catalog.reset((() =>
			Promise.resolve(
				new Response(JSON.stringify({ groups: ['a'], categories: [{ id: 1 }, null] }))
			)) as typeof fetch);
		await catalog.load('es');
		expect(catalog.state('es')?.status).toBe('error');
		catalog.reset((() => Promise.reject(new TypeError('offline'))) as typeof fetch);
		await catalog.load('es');
		expect(catalog.state('es')?.status).toBe('error');
	});

	it('gives up after the timeout', async () => {
		vi.useFakeTimers();
		catalog.reset(fixtureFetch({ hang: true }));
		const pending = catalog.load('es');
		await vi.advanceTimersByTimeAsync(10_000);
		await pending;
		expect(catalog.state('es')?.status).toBe('error');
	});

	it('ignores results that arrive after a reset', async () => {
		const fetcher = fixtureFetch({ delayMs: 5 });
		catalog.reset(fetcher);
		const pending = catalog.load('es');
		catalog.reset(fetcher);
		await pending;
		expect(catalog.state('es')).toBeUndefined();
	});

	it('falls back to the global fetch', async () => {
		const original = globalThis.fetch;
		globalThis.fetch = fixtureFetch();
		try {
			catalog.reset();
			await catalog.load('es');
			expect(catalog.state('es')?.status).toBe('ready');
		} finally {
			globalThis.fetch = original;
		}
	});
});
