import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AnswerDrafts, DEBOUNCE_MS, GRACE_DEBOUNCE_MS } from './drafts.svelte';

function setup(size = 3, initial: string[] = []) {
	const sent: [number, string][] = [];
	const drafts = new AnswerDrafts(size, (i, text) => sent.push([i, text]), initial);
	return { drafts, sent };
}

describe('AnswerDrafts', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('seeds the values from the server copy without sending anything', () => {
		const { drafts, sent } = setup(3, ['Fresa', '', 'Foca']);
		expect(drafts.values).toEqual(['Fresa', '', 'Foca']);
		expect(drafts.dirtyCount).toBe(0);
		drafts.flushAll();
		vi.advanceTimersByTime(1000);
		expect(sent).toEqual([]);
	});

	it('sends once, after the debounce, with the latest text', () => {
		const { drafts, sent } = setup();
		drafts.input(0, 'F');
		vi.advanceTimersByTime(DEBOUNCE_MS - 1);
		drafts.input(0, 'Fr');
		vi.advanceTimersByTime(DEBOUNCE_MS - 1);
		expect(sent).toEqual([]);
		vi.advanceTimersByTime(1);
		expect(sent).toEqual([[0, 'Fr']]);
		vi.advanceTimersByTime(5000);
		expect(sent).toHaveLength(1);
	});

	it('debounces every category on its own', () => {
		const { drafts, sent } = setup();
		drafts.input(0, 'Fa');
		drafts.input(2, 'Fo');
		vi.advanceTimersByTime(DEBOUNCE_MS);
		expect(sent).toEqual([
			[0, 'Fa'],
			[2, 'Fo']
		]);
	});

	it('flush sends a dirty field immediately and cancels its timer', () => {
		const { drafts, sent } = setup();
		drafts.input(1, 'Francia');
		drafts.flush(1);
		expect(sent).toEqual([[1, 'Francia']]);
		vi.advanceTimersByTime(1000);
		expect(sent).toHaveLength(1);
	});

	it('flush of a clean field sends nothing', () => {
		const { drafts, sent } = setup(2, ['Fa', '']);
		drafts.flush(0);
		drafts.flush(1);
		expect(sent).toEqual([]);
	});

	it('flushAll sends every dirty field in category order', () => {
		const { drafts, sent } = setup(4, ['', '', '', 'Foca']);
		drafts.input(2, 'Fresa');
		drafts.input(0, 'Fede');
		drafts.flushAll();
		expect(sent).toEqual([
			[0, 'Fede'],
			[2, 'Fresa']
		]);
		expect(drafts.dirtyCount).toBe(0);
		drafts.flushAll();
		expect(sent).toHaveLength(2);
	});

	it('sends trimmed text and clears a field that was emptied', () => {
		const { drafts, sent } = setup(1, ['Fa']);
		drafts.input(0, '  Fo  ');
		drafts.flush(0);
		drafts.input(0, '');
		drafts.flush(0);
		expect(sent).toEqual([
			[0, 'Fo'],
			[0, '']
		]);
	});

	it('does not send when the text goes back to what the server has', () => {
		const { drafts, sent } = setup(1, ['Fa']);
		drafts.input(0, 'Fab');
		drafts.input(0, 'Fa');
		vi.advanceTimersByTime(1000);
		expect(sent).toEqual([]);
		expect(drafts.isDirty(0)).toBe(false);
	});

	it('does not send typing that only adds trailing spaces', () => {
		const { drafts, sent } = setup(1, ['Fa']);
		drafts.input(0, 'Fa ');
		vi.advanceTimersByTime(1000);
		expect(sent).toEqual([]);
	});

	it('ignores categories out of range', () => {
		const { drafts, sent } = setup(2);
		drafts.input(5, 'x');
		drafts.input(-1, 'x');
		drafts.flushAll();
		expect(sent).toEqual([]);
		expect(drafts.size).toBe(2);
	});

	it('uses the shorter debounce when asked (grace countdown)', () => {
		const { drafts, sent } = setup();
		drafts.debounceMs = GRACE_DEBOUNCE_MS;
		drafts.input(0, 'Fa');
		vi.advanceTimersByTime(GRACE_DEBOUNCE_MS);
		expect(sent).toEqual([[0, 'Fa']]);
	});

	it('cuts the text to the server limit', () => {
		const { drafts, sent } = setup(1);
		drafts.input(0, 'f'.repeat(60));
		drafts.flush(0);
		expect(sent[0][1]).toHaveLength(40);
	});

	describe('adopt (a snapshot arrives)', () => {
		it('follows the server for fields the player has not touched', () => {
			const { drafts, sent } = setup(2);
			drafts.adopt(['Fa', 'Fo']);
			expect(drafts.values).toEqual(['Fa', 'Fo']);
			expect(drafts.dirtyCount).toBe(0);
			expect(sent).toEqual([]);
		});

		it('never overwrites what the player typed', () => {
			const { drafts, sent } = setup(2);
			drafts.input(0, 'Fres');
			drafts.adopt(['Fa', 'Fo']);
			expect(drafts.values).toEqual(['Fres', 'Fo']);
			vi.advanceTimersByTime(DEBOUNCE_MS);
			expect(sent).toEqual([[0, 'Fres']]);
		});

		it('keeps a field the player emptied, even if the server still has the old text', () => {
			const { drafts, sent } = setup(1, ['Fa']);
			drafts.input(0, '');
			drafts.adopt(['Fa']);
			expect(drafts.values).toEqual(['']);
			drafts.flush(0);
			expect(sent).toEqual([[0, '']]);
		});
	});

	describe('resync (after a reconnect)', () => {
		it('sends again what the restored server lost and adopts the rest', () => {
			const { drafts, sent } = setup(3);
			drafts.input(0, 'Fa');
			drafts.flush(0);
			drafts.input(1, 'Fo');
			drafts.flush(1);
			sent.length = 0;
			drafts.resync(['Fa', '', 'Fe']);
			expect(sent).toEqual([[1, 'Fo']]);
			expect(drafts.values).toEqual(['Fa', 'Fo', 'Fe']);
		});

		it('cancels the pending timer of a field it sends now', () => {
			const { drafts, sent } = setup(1);
			drafts.input(0, 'Fa');
			drafts.resync(['']);
			expect(sent).toEqual([[0, 'Fa']]);
			vi.advanceTimersByTime(1000);
			expect(sent).toHaveLength(1);
		});
	});

	it('dispose cancels pending sends without flushing', () => {
		const { drafts, sent } = setup();
		drafts.input(0, 'Fa');
		drafts.dispose();
		vi.advanceTimersByTime(1000);
		expect(sent).toEqual([]);
	});

	it('reset starts a new round', () => {
		const { drafts, sent } = setup(2, ['Fa', 'Fo']);
		drafts.input(0, 'x');
		drafts.reset(3, ['', 'Be']);
		vi.advanceTimersByTime(1000);
		expect(sent).toEqual([]);
		expect(drafts.values).toEqual(['', 'Be', '']);
		expect(drafts.dirtyCount).toBe(0);
	});
});
