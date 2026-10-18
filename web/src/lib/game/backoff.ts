/** First retry ceiling (ms); doubles on every failed attempt. */
export const BACKOFF_BASE_MS = 500;
/** First retry ceiling while the server drains (a deploy gap lasts a few seconds). */
export const BACKOFF_BUSY_BASE_MS = 1000;
/** No retry ceiling grows past this. */
export const BACKOFF_CAP_MS = 5000;
/** Full jitter can draw ~0; never retry faster than this. */
export const BACKOFF_MIN_MS = 100;

/**
 * Exponential backoff with full jitter: attempt `n` waits a random time in
 * `[0, min(cap, base * 2^n)]` (at least `BACKOFF_MIN_MS`). `busy` raises the
 * base to ~1 s: a 503 / close 1012 means the single server replica is being
 * replaced, and every client would otherwise retry in the same few hundred ms.
 * It is sticky until `reset()`, so a drain does not drop back to 500 ms.
 */
export class Backoff {
	#attempt = 0;
	#busy = false;
	readonly #random: () => number;

	constructor(random: () => number = Math.random) {
		this.#random = random;
	}

	/** Failed attempts since the last `reset()`. */
	get attempts(): number {
		return this.#attempt;
	}

	/** Treat the outage as a server drain from now on (starts at ~1 s). */
	markBusy(): void {
		this.#busy = true;
	}

	/** The delay (ms) before the next attempt; every call escalates the ceiling. */
	next(opts: { busy?: boolean } = {}): number {
		if (opts.busy) this.markBusy();
		const base = this.#busy ? BACKOFF_BUSY_BASE_MS : BACKOFF_BASE_MS;
		const ceiling = Math.min(BACKOFF_CAP_MS, base * 2 ** Math.min(this.#attempt, 10));
		this.#attempt++;
		return Math.max(BACKOFF_MIN_MS, Math.round(this.#random() * ceiling));
	}

	reset(): void {
		this.#attempt = 0;
		this.#busy = false;
	}
}
