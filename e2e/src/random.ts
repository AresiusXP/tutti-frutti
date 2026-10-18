/** A small seeded PRNG (mulberry32), so a failing run can be replayed with `E2E_SEED=<n>`. */
export function seeded(seed = Number(process.env.E2E_SEED ?? Date.now())): {
	seed: number;
	next: () => number;
} {
	let state = seed >>> 0;
	return {
		seed,
		next() {
			state = (state + 0x6d2b79f5) >>> 0;
			let t = state;
			t = Math.imul(t ^ (t >>> 15), t | 1);
			t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
			return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
		}
	};
}
