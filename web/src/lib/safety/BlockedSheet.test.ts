// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fieldProblems, labelNameMismatches } from '$lib/a11y.testkit';
import { i18n } from '$lib/i18n.svelte';
import { toasts } from '$lib/toast.svelte';
import BlockedSheet from './BlockedSheet.svelte';
import { BlocksStore } from './blocks.svelte';
import { apiError, deferred, FakeSafetyApi } from './safety.testkit';

const ana = { playerId: 'p1', name: 'Ana', blockedAt: 10 };
const beto = { playerId: 'p2', name: 'Beto', blockedAt: 20 };

function setup(list = [beto, ana]) {
	const api = new FakeSafetyApi();
	api.list = list;
	const blocks = new BlocksStore(api);
	const onclose = vi.fn();
	render(BlockedSheet, { blocks, onclose });
	return { api, blocks, onclose };
}

beforeEach(() => {
	i18n.set('en');
	toasts.items = [];
});
afterEach(() => cleanup());

describe('BlockedSheet', () => {
	it('lists the blocks with an Unblock button each (48 px) and says unblocking does not rejoin a game', async () => {
		setup();
		const list = await screen.findByRole('list', { name: 'Blocked players' });
		expect(
			within(list)
				.getAllByRole('listitem')
				.map((li) => li.textContent?.replace(/\s+/g, ' ').trim())
		).toEqual(['Beto Unblock', 'Ana Unblock']);
		for (const button of screen.getAllByRole('button', { name: /^Unblock / })) {
			expect(button.className).toContain('h-12');
		}
		expect(
			screen.getByText('Unblocking does not put you back in a game: it only lets you meet again.')
		).toBeTruthy();
		expect(labelNameMismatches(screen.getByRole('dialog'))).toEqual([]);
		expect(fieldProblems(screen.getByRole('dialog'))).toEqual([]);
	});

	it('shows the empty state', async () => {
		setup([]);
		expect(await screen.findByText("You haven't blocked anyone.")).toBeTruthy();
		expect(screen.queryByRole('list', { name: 'Blocked players' })).toBeNull();
	});

	it('shows loading, then an error with a Retry that loads again', async () => {
		const api = new FakeSafetyApi();
		const pending = deferred<(typeof ana)[]>();
		api.listAnswer = pending.promise;
		const blocks = new BlocksStore(api);
		render(BlockedSheet, { blocks, onclose: () => undefined });
		expect((await screen.findByRole('status')).textContent).toBe('Loading the list…');
		pending.reject(apiError(0, 'network'));
		expect((await screen.findByRole('alert')).textContent).toContain("Can't reach the server.");
		api.listAnswer = [ana];
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		expect(await screen.findByText('Ana')).toBeTruthy();
	});

	it('Unblock removes the entry after the server answered and toasts', async () => {
		const { api } = setup();
		await fireEvent.click(await screen.findByRole('button', { name: 'Unblock Ana' }));
		await waitFor(() => expect(screen.queryByText('Ana')).toBeNull());
		expect(api.unblocked).toEqual(['p1']);
		expect(screen.getByText('Beto')).toBeTruthy();
		expect(toasts.items.map((t) => t.text)).toEqual(['Ana unblocked.']);
	});

	it('a failed Unblock keeps the entry and says why', async () => {
		const { api } = setup();
		api.unblockAnswer = apiError(503, 'server_busy');
		await fireEvent.click(await screen.findByRole('button', { name: 'Unblock Ana' }));
		expect((await screen.findByRole('alert')).textContent).toContain('The server is busy');
		expect(screen.getByText('Ana')).toBeTruthy();
		expect(toasts.items).toHaveLength(0);
	});

	it('names a block that was stored without a nickname', async () => {
		setup([{ playerId: 'p9', name: '', blockedAt: 1 }]);
		expect(await screen.findByRole('button', { name: 'Unblock Unnamed player' })).toBeTruthy();
	});

	it('Escape closes it', async () => {
		const { onclose } = setup();
		await screen.findByRole('dialog', { name: 'Blocked players' });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onclose).toHaveBeenCalled();
	});

	it.each([
		['es', 'Jugadores bloqueados', 'No bloqueaste a nadie.'],
		['nl', 'Geblokkeerde spelers', 'Je hebt niemand geblokkeerd.']
	] as const)('speaks %s', async (locale, title, empty) => {
		i18n.set(locale);
		setup([]);
		expect(await screen.findByRole('dialog', { name: title })).toBeTruthy();
		expect(await screen.findByText(empty)).toBeTruthy();
	});
});
