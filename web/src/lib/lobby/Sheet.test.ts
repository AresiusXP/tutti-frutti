// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { fakeRouter } from './router.testkit.svelte';
import SheetHarness from './SheetHarness.svelte';

const router = vi.hoisted(() => ({ pushState: vi.fn() }));
vi.mock('$app/navigation', () => ({ pushState: router.pushState }));
vi.mock('$app/state', async () => ({ page: (await import('./router.testkit.svelte')).page }));

describe('Sheet and the Back button', () => {
	let back: ReturnType<typeof vi.spyOn>;
	const onclosed = vi.fn();

	beforeEach(() => {
		i18n.set('es');
		fakeRouter.reset();
		onclosed.mockClear();
		router.pushState.mockReset().mockImplementation(fakeRouter.pushState);
		back = vi.spyOn(history, 'back').mockImplementation(fakeRouter.back);
	});
	afterEach(() => vi.restoreAllMocks());

	const open = async (name = 'open outer') => {
		await fireEvent.click(screen.getByRole('button', { name }));
	};
	const dialogs = () => screen.queryAllByRole('dialog');

	it('opening pushes exactly one entry for the same URL', async () => {
		render(SheetHarness, { onclosed });
		expect(router.pushState).not.toHaveBeenCalled();
		await open();
		expect(dialogs()).toHaveLength(1);
		expect(router.pushState).toHaveBeenCalledOnce();
		expect(router.pushState).toHaveBeenCalledWith('', { tfSheet: 1 });
		expect(fakeRouter.depth).toBe(1);
	});

	it('Back closes the sheet and leaves the history as it was (no extra pop, no leave)', async () => {
		render(SheetHarness, { onclosed });
		await open();
		fakeRouter.back();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(onclosed).toHaveBeenCalledExactlyOnceWith('outer');
		expect(back).not.toHaveBeenCalled();
		expect(fakeRouter.depth).toBe(0);
	});

	it.each([
		['the close button', () => fireEvent.click(screen.getByRole('button', { name: 'Cerrar' }))],
		['Escape', () => fireEvent.keyDown(window, { key: 'Escape' })],
		['the backdrop', () => fireEvent.click(screen.getByRole('dialog').parentElement as HTMLElement)]
	])('%s pops our own entry, so the history does not grow', async (_name, close) => {
		render(SheetHarness, { onclosed });
		await open();
		await close();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(back).toHaveBeenCalledOnce();
		expect(fakeRouter.depth).toBe(0);
		expect(onclosed).toHaveBeenCalledOnce();
	});

	it('open, close, open, Back: one entry at a time', async () => {
		render(SheetHarness, { onclosed });
		await open();
		await fireEvent.click(screen.getByRole('button', { name: 'Cerrar' }));
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		await open();
		expect(fakeRouter.depth).toBe(1);
		fakeRouter.back();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(fakeRouter.depth).toBe(0);
		expect(router.pushState).toHaveBeenCalledTimes(2);
	});

	it('a real navigation closes it without popping anything (the entry is no longer ours)', async () => {
		render(SheetHarness, { onclosed });
		await open();
		fakeRouter.navigate();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(back).not.toHaveBeenCalled();
		expect(fakeRouter.depth).toBe(2);
	});

	it('with two sheets Back closes only the top one', async () => {
		render(SheetHarness, { onclosed });
		await open();
		await open('open inner');
		expect(dialogs()).toHaveLength(2);
		expect(router.pushState).toHaveBeenLastCalledWith('', { tfSheet: 2 });

		fakeRouter.back();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(1));
		expect(onclosed).toHaveBeenCalledExactlyOnceWith('inner');
		expect(screen.getByRole('dialog').getAttribute('aria-labelledby')).toBeTruthy();
		expect(screen.getByRole('heading', { name: 'Outer' })).toBeTruthy();

		fakeRouter.back();
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(onclosed).toHaveBeenLastCalledWith('outer');
		expect(fakeRouter.depth).toBe(0);
		expect(back).not.toHaveBeenCalled();
	});

	it('closing the top sheet by its button pops exactly its entry and keeps the one below', async () => {
		render(SheetHarness, { onclosed });
		await open();
		await open('open inner');
		const closes = screen.getAllByRole('button', { name: 'Cerrar' });
		await fireEvent.click(closes[1]);
		await vi.waitFor(() => expect(dialogs()).toHaveLength(1));
		expect(back).toHaveBeenCalledOnce();
		expect(fakeRouter.depth).toBe(1);
		expect(screen.getByRole('heading', { name: 'Outer' })).toBeTruthy();
	});

	it('with two sheets one Escape closes only the top one and the history stays consistent', async () => {
		render(SheetHarness, { onclosed });
		await open();
		await open('open inner');
		await fireEvent.keyDown(window, { key: 'Escape' });
		await vi.waitFor(() => expect(dialogs()).toHaveLength(1));
		expect(onclosed).toHaveBeenCalledExactlyOnceWith('inner');
		expect(screen.getByRole('heading', { name: 'Outer' })).toBeTruthy();
		// the entry of the sheet below is still there, exactly one pop happened
		expect(back).toHaveBeenCalledOnce();
		expect(fakeRouter.depth).toBe(1);
		// a second Escape now closes the one that is left, and nothing stale remains
		await fireEvent.keyDown(window, { key: 'Escape' });
		await vi.waitFor(() => expect(dialogs()).toHaveLength(0));
		expect(onclosed).toHaveBeenLastCalledWith('outer');
		expect(back).toHaveBeenCalledTimes(2);
		expect(fakeRouter.depth).toBe(0);
	});

	it('with two sheets Tab is trapped by the top one only', async () => {
		render(SheetHarness, { onclosed });
		await open();
		await open('open inner');
		const inner = screen.getAllByRole('dialog')[1];
		const closeInner = [...inner.querySelectorAll('button')].at(-1) as HTMLElement;
		closeInner.focus();
		await fireEvent.keyDown(window, { key: 'Tab' });
		// the focus wrapped inside the inner sheet and never reached the outer one
		expect(inner.contains(document.activeElement)).toBe(true);
	});
});
