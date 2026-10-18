/**
 * Catalog fixtures for tests and the dev playground: the real
 * `server/internal/catalog/categories.json` (copied to `categories.fixture.json`;
 * a test fails when they drift) served the way `GET /api/categories?lang=` does.
 */
import type { Category, CategoryRef, Lang, Listing } from '$lib/protocol';
import raw from './categories.fixture.json';

export const FIXTURE_CATEGORIES: readonly Category[] = raw;

/** What `GET /api/categories?lang=<lang>` returns. */
export function listingFor(lang: Lang): Listing {
	const groups: string[] = [];
	for (const category of FIXTURE_CATEGORIES) {
		if (!groups.includes(category.group)) groups.push(category.group);
	}
	return {
		lang,
		groups,
		categories: FIXTURE_CATEGORIES.map((c) => ({
			id: c.id,
			group: c.group,
			label: lang === 'en' ? c.en : c.es,
			default: c.default
		}))
	};
}

/** The labels the server would put in `view.categoryLabels` for `refs`. */
export function labelsFor(lang: Lang, refs: readonly CategoryRef[]): string[] {
	const byId = new Map(listingFor(lang).categories.map((c) => [c.id, c.label]));
	return refs.map((ref) => ref.custom ?? byId.get(ref.id ?? '') ?? ref.id ?? '');
}

export interface FixtureFetchOptions {
	/** Milliseconds before every response (default 0). */
	delayMs?: number;
	/** The first N requests fail with a 503 (then they work): shows the error + Retry state. */
	failFirst?: number;
	/** Requests never complete (the loading state). */
	hang?: boolean;
}

/** A `fetch` that answers `/api/categories?lang=` from the fixture and counts its calls. */
export function fixtureFetch(options: FixtureFetchOptions = {}) {
	let calls = 0;
	const fn = ((input: RequestInfo | URL, init?: RequestInit) => {
		calls++;
		const attempt = calls;
		const url = new URL(String(input), 'http://localhost');
		return new Promise<Response>((resolve, reject) => {
			if (options.hang) {
				init?.signal?.addEventListener('abort', () =>
					reject(new DOMException('aborted', 'AbortError'))
				);
				return;
			}
			const respond = () => {
				if (attempt <= (options.failFirst ?? 0)) {
					resolve(new Response('{"error":"server_busy"}', { status: 503 }));
					return;
				}
				const lang = url.searchParams.get('lang');
				if (url.pathname !== '/api/categories' || (lang !== 'es' && lang !== 'en')) {
					resolve(new Response('{"error":"not_found"}', { status: 404 }));
					return;
				}
				resolve(
					new Response(JSON.stringify(listingFor(lang)), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			};
			if (options.delayMs) setTimeout(respond, options.delayMs);
			else respond();
		});
	}) as typeof fetch & { calls: () => number; urls: string[] };
	return Object.assign(fn, { calls: () => calls });
}
