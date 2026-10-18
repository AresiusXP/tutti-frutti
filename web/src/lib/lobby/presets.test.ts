import { describe, expect, it } from 'vitest';
import { listingFor } from './fixtures';
import { classicRefs, shuffle, surpriseCount, surpriseRefs } from './presets';

const listing = listingFor('es');

/** A deterministic RNG cycling through the given values. */
function seq(...values: number[]) {
	let i = 0;
	return () => values[i++ % values.length];
}

describe('shuffle', () => {
	it('returns a permutation and never mutates its input', () => {
		const input = [1, 2, 3, 4, 5];
		const out = shuffle(input, seq(0.1, 0.9, 0.5, 0.3));
		expect(input).toEqual([1, 2, 3, 4, 5]);
		expect([...out].sort()).toEqual([1, 2, 3, 4, 5]);
	});

	it('is deterministic for a given RNG and handles the 1.0 edge', () => {
		expect(shuffle([1, 2, 3], () => 0)).toEqual([2, 3, 1]);
		expect(shuffle([1, 2, 3], () => 0.999999)).toEqual([1, 2, 3]);
		expect(shuffle([1, 2, 3], () => 1)).toEqual([1, 2, 3]);
		expect(shuffle([])).toEqual([]);
		expect(shuffle([7])).toEqual([7]);
	});
});

describe('presets', () => {
	it('Clásicas are the six defaults in catalog order', () => {
		expect(classicRefs(listing)).toEqual(
			['nombre', 'pais', 'animal', 'comida', 'color', 'marca'].map((id) => ({ id }))
		);
	});

	it('Sorprendeme keeps the current count', () => {
		const refs = surpriseRefs(listing, 8, seq(0.2, 0.7, 0.4));
		expect(refs).toHaveLength(8);
		expect(new Set(refs.map((r) => r.id)).size).toBe(8);
		for (const ref of refs) expect(listing.categories.some((c) => c.id === ref.id)).toBe(true);
	});

	it('Sorprendeme uses 6 when fewer than 3 are selected and never more than 10', () => {
		expect(surpriseCount(0)).toBe(6);
		expect(surpriseCount(2)).toBe(6);
		expect(surpriseCount(3)).toBe(3);
		expect(surpriseCount(10)).toBe(10);
		expect(surpriseCount(14)).toBe(10);
		expect(surpriseRefs(listing, 1, seq(0.5))).toHaveLength(6);
		expect(surpriseRefs(listing, 3)).toHaveLength(3);
	});

	it('differs between runs of different randomness', () => {
		const a = surpriseRefs(listing, 6, () => 0.1);
		const b = surpriseRefs(listing, 6, () => 0.8);
		expect(a).not.toEqual(b);
	});
});
