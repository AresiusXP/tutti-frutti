import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OnceCommand } from './once.svelte';

const err = (seq: number, ref?: string) => ({ code: 'x', ref, at: 0, seq });

describe('OnceCommand', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('runs the command once', () => {
		const once = new OnceCommand(['basta']);
		const run = vi.fn();
		expect(once.fire(0, run)).toBe(true);
		expect(once.fire(0, run)).toBe(false);
		expect(once.fire(0, run)).toBe(false);
		expect(run).toHaveBeenCalledTimes(1);
		expect(once.locked).toBe(true);
	});

	it('unlocks on a new error for the command, not on old or unrelated ones', () => {
		const once = new OnceCommand(['basta']);
		once.fire(4, () => {});
		once.failed(null);
		once.failed(err(4, 'basta'));
		once.failed(err(5, 'set_answer'));
		expect(once.locked).toBe(true);
		once.failed(err(6, 'basta'));
		expect(once.locked).toBe(false);
	});

	it('unlocks on a new error without a ref', () => {
		const once = new OnceCommand(['basta']);
		once.fire(0, () => {});
		once.failed(err(1));
		expect(once.locked).toBe(false);
	});

	it('re-arms itself after the retry delay when asked to', () => {
		const once = new OnceCommand(['basta'], 4000);
		const run = vi.fn();
		once.fire(0, run);
		vi.advanceTimersByTime(3999);
		expect(once.locked).toBe(true);
		vi.advanceTimersByTime(1);
		expect(once.locked).toBe(false);
		expect(once.fire(0, run)).toBe(true);
		expect(run).toHaveBeenCalledTimes(2);
	});

	it('never re-arms without a retry delay, and dispose cancels the timer', () => {
		const forever = new OnceCommand(['stop_letter']);
		forever.fire(0, () => {});
		vi.advanceTimersByTime(60_000);
		expect(forever.locked).toBe(true);
		const once = new OnceCommand(['basta'], 1000);
		once.fire(0, () => {});
		once.dispose();
		vi.advanceTimersByTime(5000);
		expect(once.locked).toBe(true);
	});
});
