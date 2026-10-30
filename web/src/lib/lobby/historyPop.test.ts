// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { expectPop, historySettled } from './historyPop';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('historySettled', () => {
	it('is already settled when nothing is popping', async () => {
		await expect(historySettled()).resolves.toBeUndefined();
	});

	it('waits for the popstate of the closed sheet, then for the router to start its own navigation', async () => {
		expectPop();
		let done = false;
		void historySettled().then(() => (done = true));
		await vi.advanceTimersByTimeAsync(100);
		expect(done).toBe(false);
		window.dispatchEvent(new PopStateEvent('popstate'));
		await vi.advanceTimersByTimeAsync(0);
		expect(done).toBe(true);
	});

	it('does not wait forever when no popstate comes (the entry was already gone)', async () => {
		expectPop();
		let done = false;
		void historySettled().then(() => (done = true));
		await vi.advanceTimersByTimeAsync(399);
		expect(done).toBe(false);
		await vi.advanceTimersByTimeAsync(10);
		expect(done).toBe(true);
	});

	it('is settled again afterwards and leaves no listener behind', async () => {
		const remove = vi.spyOn(window, 'removeEventListener');
		expectPop();
		window.dispatchEvent(new PopStateEvent('popstate'));
		await vi.advanceTimersByTimeAsync(10);
		expect(remove).toHaveBeenCalledWith('popstate', expect.any(Function));
		let done = false;
		void historySettled().then(() => (done = true));
		await vi.advanceTimersByTimeAsync(0);
		expect(done).toBe(true);
	});
});
