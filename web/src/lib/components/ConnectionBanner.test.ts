// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import ConnectionBanner from './ConnectionBanner.svelte';

describe('ConnectionBanner', () => {
	beforeEach(() => i18n.set('es'));

	it('is an empty polite live region while connected', () => {
		render(ConnectionBanner, { session: new FakeSession(makeView()) });
		const region = screen.getByRole('status');
		expect(region.getAttribute('aria-live')).toBe('polite');
		expect(region.textContent?.trim()).toBe('');
	});

	it('announces "Reconectando…" while reconnecting and goes away when connected', async () => {
		const session = new FakeSession(makeView());
		render(ConnectionBanner, { session });
		session.status = 'reconnecting';
		await tick();
		expect(screen.getByRole('status').textContent).toContain('Reconectando…');
		session.status = 'connected';
		await tick();
		expect(screen.getByRole('status').textContent?.trim()).toBe('');
	});

	it('also covers a first connect after a drop (connecting with a view)', async () => {
		const session = new FakeSession(makeView());
		session.status = 'connecting';
		render(ConnectionBanner, { session });
		expect(screen.getByRole('status').textContent).toContain('Reconectando…');
	});

	it('does not cover the very first connect (no view yet) nor a closed session', async () => {
		const first = new FakeSession(null);
		first.status = 'connecting';
		const { unmount } = render(ConnectionBanner, { session: first });
		expect(screen.getByRole('status').textContent?.trim()).toBe('');
		unmount();
		const closed = new FakeSession(makeView());
		closed.status = 'closed';
		render(ConnectionBanner, { session: closed });
		expect(screen.getByRole('status').textContent?.trim()).toBe('');
	});

	it('is translated and never intercepts touches', async () => {
		i18n.set('en');
		const session = new FakeSession(makeView());
		session.status = 'reconnecting';
		render(ConnectionBanner, { session });
		const region = screen.getByRole('status');
		expect(region.textContent).toContain('Reconnecting…');
		expect(region.className).toContain('pointer-events-none');
		i18n.set('es');
	});

	it('stays quiet while the browser is offline (the offline banner covers it)', async () => {
		const session = new FakeSession(makeView());
		session.status = 'reconnecting';
		render(ConnectionBanner, { session });
		window.dispatchEvent(new Event('offline'));
		await tick();
		expect(screen.getByRole('status').textContent?.trim()).toBe('');
		window.dispatchEvent(new Event('online'));
		await tick();
		expect(screen.getByRole('status').textContent).toContain('Reconectando…');
	});
});
