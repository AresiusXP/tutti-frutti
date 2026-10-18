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
		expect(screen.queryByText('Conectando…')).toBeNull();
		await vi.advanceTimersByTimeAsync(1);
		expect(screen.getByText('Conectando…')).toBeTruthy();
		expect(screen.getByText('Entrando a la sala KT7P')).toBeTruthy();
	});

	it('is a polite status region and clears its timer when it goes away', async () => {
		const { unmount } = render(ConnectingScreen, { code: 'KT7P' });
		expect(screen.getByRole('status').getAttribute('aria-live')).toBe('polite');
		unmount();
		expect(vi.getTimerCount()).toBe(0);
	});
});
