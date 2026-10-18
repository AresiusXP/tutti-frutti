import type { Lang, Listing } from '$lib/protocol';

export type CatalogState =
	{ status: 'loading' } | { status: 'ready'; listing: Listing } | { status: 'error' };

const TIMEOUT_MS = 10_000;

function isListing(value: unknown): value is Listing {
	if (typeof value !== 'object' || value === null) return false;
	const v = value as Record<string, unknown>;
	return (
		Array.isArray(v.groups) &&
		v.groups.every((g) => typeof g === 'string') &&
		Array.isArray(v.categories) &&
		v.categories.every((c) => {
			if (typeof c !== 'object' || c === null) return false;
			const entry = c as Record<string, unknown>;
			return (
				typeof entry.id === 'string' &&
				typeof entry.group === 'string' &&
				typeof entry.label === 'string' &&
				typeof entry.default === 'boolean'
			);
		})
	);
}

/**
 * The category catalog (`GET /api/categories?lang=`), cached per language for
 * the life of the page. The listing is immutable server data, so a ready
 * language is never fetched again; a failed one can be retried.
 */
export class CatalogStore {
	#states = $state<Partial<Record<Lang, CatalogState>>>({});
	#inflight: Partial<Record<Lang, Promise<void>>> = {};
	#generation = 0;
	/** Test / playground override of `fetch`. */
	fetcher: typeof fetch | null = null;

	state(lang: Lang): CatalogState | undefined {
		return this.#states[lang];
	}

	listing(lang: Lang): Listing | null {
		const state = this.#states[lang];
		return state?.status === 'ready' ? state.listing : null;
	}

	/** Loads `lang` unless it is ready or already loading. An earlier error is retried. */
	load(lang: Lang): Promise<void> {
		if (this.#states[lang]?.status === 'ready') return Promise.resolve();
		const running = this.#inflight[lang];
		if (running) return running;
		this.#states[lang] = { status: 'loading' };
		const generation = this.#generation;
		const task = this.#fetch(lang).then(
			(listing) => {
				if (generation === this.#generation) this.#states[lang] = { status: 'ready', listing };
			},
			() => {
				if (generation === this.#generation) this.#states[lang] = { status: 'error' };
			}
		);
		const tracked = task.finally(() => {
			if (this.#inflight[lang] === tracked) delete this.#inflight[lang];
		});
		this.#inflight[lang] = tracked;
		return tracked;
	}

	async #fetch(lang: Lang): Promise<Listing> {
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
		try {
			const doFetch = this.fetcher ?? fetch;
			const response = await doFetch(`/api/categories?lang=${lang}`, {
				signal: controller.signal,
				headers: { Accept: 'application/json' }
			});
			if (!response.ok) throw new Error(`categories ${response.status}`);
			const body: unknown = await response.json();
			if (!isListing(body)) throw new Error('categories: unexpected body');
			return body;
		} finally {
			clearTimeout(timer);
		}
	}

	/** Forgets everything (tests and the playground). In-flight results are ignored. */
	reset(fetcher: typeof fetch | null = null): void {
		this.#generation++;
		this.#states = {};
		this.#inflight = {};
		this.fetcher = fetcher;
	}
}

export const catalog = new CatalogStore();
