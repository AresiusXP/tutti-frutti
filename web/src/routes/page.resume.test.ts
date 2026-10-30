// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { account } from '$lib/account';
import type { FakeAccount } from '$lib/account/fake.svelte';
import { ApiError, api, type RoomInfo } from '$lib/api';
import { clearRoomMark, hasRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { RECENT_ROOM_KEY, rememberRecentRoom } from '$lib/resume/recent';
import { TERMS_OK } from '$lib/safety/safety.testkit';
import { terms } from '$lib/safety/terms.svelte';
import { toasts } from '$lib/toast.svelte';
import Home from './+page.svelte';

const route = vi.hoisted(() => ({ url: new URL('http://localhost/'), state: {} }));

vi.mock('$app/navigation', async () => {
	const { onMount } = await import('svelte');
	return {
		goto: vi.fn(() => Promise.resolve()),
		replaceState: vi.fn(),
		afterNavigate: (callback: () => void) => onMount(() => callback())
	};
});
vi.mock('$app/state', () => ({ page: route }));
vi.mock('$lib/account', async (importOriginal) => {
	const { FakeAccount } = await import('$lib/account/fake.svelte');
	return { ...(await importOriginal<typeof import('$lib/account')>()), account: new FakeAccount() };
});
vi.mock('$lib/api', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api')>()),
	api: { createRoom: vi.fn(), getRoom: vi.fn(), ensureSession: vi.fn() }
}));

const ID = '01020304-0506-4708-890a-0b0c0d0e0f10';
const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	visibility: 'private',
	roomInstanceId: ID,
	...over
});
const fake = account as unknown as FakeAccount;
const resume = () => screen.queryByRole('button', { name: /^Retomar la partida reciente/ });

beforeEach(() => {
	i18n.set('es');
	profile.setNickname('Ana');
	toasts.items = [];
	clearRoomMark('KT7P');
	localStorage.clear();
	fake.reset();
	terms.apply(TERMS_OK);
	vi.mocked(goto).mockClear();
	vi.mocked(api.createRoom).mockReset();
	vi.mocked(api.getRoom).mockReset().mockResolvedValue(info());
	route.url = new URL('http://localhost/');
});
afterEach(() => {
	cleanup();
	clearRoomMark('KT7P');
	localStorage.clear();
});

describe('Home: Resume recent game', () => {
	it('is not there when this browser remembers no game, and nobody is asked', async () => {
		render(Home);
		await Promise.resolve();
		expect(resume()).toBeNull();
		expect(api.getRoom).not.toHaveBeenCalled();
		expect(screen.getByRole('button', { name: 'Creá una partida' })).toBeTruthy();
	});

	it('shows the room code only, after the server confirmed the same room, and does not navigate by itself', async () => {
		rememberRecentRoom('KT7P', ID);
		render(Home);
		const card = await screen.findByRole('button', { name: 'Retomar la partida reciente KT7P' });
		expect(api.getRoom).toHaveBeenCalledWith('KT7P', { signal: expect.any(AbortSignal) });
		expect(card.textContent).not.toContain('Ana');
		expect(goto).not.toHaveBeenCalled();
		expect(hasRoomMark('KT7P')).toBe(false);
	});

	it('the tap marks this tab and opens the room route, with no second lookup and no terms gate', async () => {
		rememberRecentRoom('KT7P', ID);
		render(Home);
		await fireEvent.click(await screen.findByRole('button', { name: /KT7P/ }));
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(hasRoomMark('KT7P')).toBe(true);
		expect(api.getRoom).toHaveBeenCalledTimes(1);
		expect(terms.open).toBe(false);
		expect(toasts.items).toEqual([]);
	});

	it.each([
		['full', info({ players: 10, joinable: false })],
		['finished', info({ phase: 'final', joinable: false })],
		['in progress', info({ phase: 'playing', players: 3, joinable: false })]
	])(
		'a %s room is still offered: a seated player reclaims the seat, the server decides at the join',
		async (_name, room) => {
			vi.mocked(api.getRoom).mockResolvedValue(room);
			rememberRecentRoom('KT7P', ID);
			render(Home);
			await fireEvent.click(await screen.findByRole('button', { name: /KT7P/ }));
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
			expect(toasts.items).toEqual([]); // no "full" / "over" refusal: that is for a new join by code
		}
	);

	it('a new join by code to a full room still fails gracefully (the Join action is unchanged)', async () => {
		vi.mocked(api.getRoom).mockResolvedValue(info({ code: 'BCDF', players: 10, joinable: false }));
		render(Home);
		for (const [i, ch] of ['B', 'C', 'D', 'F'].entries()) {
			await fireEvent.input(screen.getByLabelText(`Carácter ${i + 1} de 4`), {
				target: { value: ch }
			});
		}
		await fireEvent.click(screen.getByRole('button', { name: 'Unite a una partida' }));
		await waitFor(() => expect(toasts.items.map((t) => t.text)).toEqual(['La sala está llena']));
		expect(goto).not.toHaveBeenCalled();
	});

	it('404: no card, the entry is forgotten and Home works as usual', async () => {
		vi.mocked(api.getRoom).mockRejectedValue(new ApiError(404, 'room_not_found'));
		rememberRecentRoom('KT7P', ID);
		render(Home);
		await waitFor(() => expect(localStorage.getItem(RECENT_ROOM_KEY)).toBeNull());
		expect(resume()).toBeNull();
		expect(toasts.items).toEqual([]);
		expect(
			(screen.getByRole('button', { name: 'Creá una partida' }) as HTMLButtonElement).disabled
		).toBe(false);
	});

	it('a code reused by another room (different instance id): no card, forgotten', async () => {
		vi.mocked(api.getRoom).mockResolvedValue(
			info({ roomInstanceId: '11121314-1516-4718-991a-1b1c1d1e1f20' })
		);
		rememberRecentRoom('KT7P', ID);
		render(Home);
		await waitFor(() => expect(localStorage.getItem(RECENT_ROOM_KEY)).toBeNull());
		expect(resume()).toBeNull();
	});

	it('offline (the check fails): the card is shown anyway and the tap still goes to the room route', async () => {
		vi.mocked(api.getRoom).mockRejectedValue(new ApiError(0, 'network'));
		rememberRecentRoom('KT7P', ID);
		render(Home);
		await fireEvent.click(await screen.findByRole('button', { name: /KT7P/ }));
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(localStorage.getItem(RECENT_ROOM_KEY)).not.toBeNull();
	});

	it('an entry older than 24 h is not offered and is removed', async () => {
		rememberRecentRoom('KT7P', ID, Date.now() - 25 * 3600_000);
		render(Home);
		await Promise.resolve();
		expect(resume()).toBeNull();
		expect(api.getRoom).not.toHaveBeenCalled();
		expect(localStorage.getItem(RECENT_ROOM_KEY)).toBeNull();
	});

	it('on a short phone Home drops its tagline while the card is offered, so it still fits one screen', async () => {
		const tagline = () => screen.getByText(/Anotá una palabra por categoría/);
		rememberRecentRoom('KT7P', ID);
		render(Home);
		expect(tagline().className).not.toContain('hidden');
		await screen.findByRole('button', { name: /KT7P/ });
		// `hidden tall:block`: gone below 800 px of height, back on a tall screen
		await waitFor(() => expect(tagline().className).toContain('hidden tall:block'));
	});

	it('keeps the tagline when there is nothing to resume', async () => {
		vi.mocked(api.getRoom).mockRejectedValue(new ApiError(404, 'room_not_found'));
		rememberRecentRoom('KT7P', ID);
		render(Home);
		await waitFor(() => expect(localStorage.getItem(RECENT_ROOM_KEY)).toBeNull());
		expect(screen.getByText(/Anotá una palabra por categoría/).className).not.toContain('hidden');
	});

	it('while a create is in flight the tap does nothing', async () => {
		vi.mocked(api.createRoom).mockReturnValue(new Promise(() => undefined));
		rememberRecentRoom('KT7P', ID);
		render(Home);
		const card = await screen.findByRole('button', { name: /KT7P/ });
		await fireEvent.click(screen.getByRole('button', { name: 'Creá una partida' }));
		await fireEvent.click(card);
		expect(goto).not.toHaveBeenCalled();
	});

	it('is in the three languages (no Spanish word on the English or Dutch Home)', async () => {
		rememberRecentRoom('KT7P', ID);
		for (const [locale, name] of [
			['en', 'Resume recent game KT7P'],
			['nl', 'Recent spel hervatten KT7P']
		] as const) {
			i18n.set(locale);
			const { unmount } = render(Home);
			expect(await screen.findByRole('button', { name })).toBeTruthy();
			unmount();
		}
	});
});
