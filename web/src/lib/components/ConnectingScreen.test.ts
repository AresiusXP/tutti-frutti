// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import ConnectingScreen from './ConnectingScreen.svelte';

describe('ConnectingScreen', () => {
	beforeEach(() => {
		i18n.set('es');
		vi.useFakeTimers();
	});
	afterEach(() => vi.useRealTimers());

	it('draws nothing for the first 300 ms (no flash on a fast connect)', async () => {
		render(ConnectingScreen, { code: 'KT7P' });
		await vi.advanceTimersByTimeAsync(299);
		// only the screen-reader-only heading is there: nothing to see yet
		expect(screen.queryByText('Entrando a la sala KT7P')).toBeNull();
		expect(screen.getByText('Conectando…').classList.contains('sr-only')).toBe(true);
		await vi.advanceTimersByTimeAsync(1);
		expect(screen.getByText('Conectando…').classList.contains('sr-only')).toBe(false);
		expect(screen.getByText('Entrando a la sala KT7P')).toBeTruthy();
	});

	it('always has exactly one h1: hidden at first, the visible title once it is drawn', async () => {
		render(ConnectingScreen, { code: 'KT7P' });
		const hidden = screen.getByRole('heading', { level: 1 });
		expect(hidden.textContent).toBe('Conectando…');
		expect(hidden.classList.contains('sr-only')).toBe(true);
		await vi.advanceTimersByTimeAsync(300);
		const shown = screen.getByRole('heading', { level: 1 });
		expect(shown.textContent).toBe('Conectando…');
		expect(shown.classList.contains('sr-only')).toBe(false);
	});

	it('is a polite status region and clears its timer when it goes away', async () => {
		const { unmount } = render(ConnectingScreen, { code: 'KT7P' });
		expect(screen.getByRole('status').getAttribute('aria-live')).toBe('polite');
		unmount();
		expect(vi.getTimerCount()).toBe(0);
	});
});
