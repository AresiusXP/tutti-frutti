import type { CategoryRef, Listing } from '$lib/protocol';
import { MAX_CATEGORIES, MIN_CATEGORIES } from './labels';

/** Source of randomness in [0, 1) (inject a fake one in tests). */
export type Rng = () => number;

export const SURPRISE_DEFAULT_COUNT = 6;

/** Fisher–Yates: a shuffled copy. */
export function shuffle<T>(items: readonly T[], rng: Rng = Math.random): T[] {
	const out = [...items];
	for (let i = out.length - 1; i > 0; i--) {
		const j = Math.min(Math.floor(rng() * (i + 1)), i);
		[out[i], out[j]] = [out[j], out[i]];
	}
	return out;
}

/** "Clásicas": the catalog's default categories, in catalog order. */
export function classicRefs(listing: Listing): CategoryRef[] {
	return listing.categories.filter((c) => c.default).map((c) => ({ id: c.id }));
}

/** How many categories "Sorprendeme" picks: the current count, or 6 when there are fewer than 3. */
export function surpriseCount(current: number): number {
	return current < MIN_CATEGORIES ? SURPRISE_DEFAULT_COUNT : Math.min(current, MAX_CATEGORIES);
}

/** "Sorprendeme": `count` distinct random built-in categories. */
export function surpriseRefs(
	listing: Listing,
	currentCount: number,
	rng: Rng = Math.random
): CategoryRef[] {
	return shuffle(listing.categories, rng)
		.slice(0, surpriseCount(currentCount))
		.map((c) => ({ id: c.id }));
}
