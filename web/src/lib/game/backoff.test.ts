import { describe, expect, it } from 'vitest';
import {
	BACKOFF_BASE_MS,
	BACKOFF_BUSY_BASE_MS,
	BACKOFF_CAP_MS,
	BACKOFF_MIN_MS,
	Backoff
} from './backoff';

const sequence = (b: Backoff, n: number, opts?: { busy?: boolean }) =>
	Array.from({ length: n }, () => b.next(opts));

describe('Backoff', () => {
	it('doubles from 500 ms up to the 5 s cap (random = 1 gives the ceiling)', () => {
		expect(sequence(new Backoff(() => 1), 8)).toEqual([
			500, 1000, 2000, 4000, 5000, 5000, 5000, 5000
		]);
		expect(BACKOFF_BASE_MS).toBe(500);
		expect(BACKOFF_CAP_MS).toBe(5000);
	});

	it('applies full jitter: the delay is random * ceiling, never below the floor', () => {
		expect(sequence(new Backoff(() => 0.5), 4)).toEqual([250, 500, 1000, 2000]);
		expect(sequence(new Backoff(() => 0), 3)).toEqual([
			BACKOFF_MIN_MS,
			BACKOFF_MIN_MS,
			BACKOFF_MIN_MS
		]);
		const rolls = [0.1, 0.9, 0.5];
		let i = 0;
		expect(sequence(new Backoff(() => rolls[i++]), 3)).toEqual([100, 900, 1000]);
	});

	it('starts at ~1 s once the server is draining, and stays there until reset', () => {
		const b = new Backoff(() => 1);
		expect(b.next({ busy: true })).toBe(BACKOFF_BUSY_BASE_MS);
		expect(b.next()).toBe(2000); // sticky
		expect(b.next()).toBe(4000);
		expect(b.next()).toBe(5000);
		b.reset();
		expect(b.attempts).toBe(0);
		expect(b.next()).toBe(500);
	});

	it('markBusy() has the same effect as busy', () => {
		const b = new Backoff(() => 1);
		b.markBusy();
		expect(b.next()).toBe(1000);
	});

	it('counts attempts and never overflows', () => {
		const b = new Backoff(() => 1);
		sequence(b, 100);
		expect(b.attempts).toBe(100);
		expect(b.next()).toBe(5000);
	});

	it('uses Math.random by default', () => {
		const delay = new Backoff().next();
		expect(delay).toBeGreaterThanOrEqual(BACKOFF_MIN_MS);
		expect(delay).toBeLessThanOrEqual(500);
	});
});
