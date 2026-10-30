// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { ApiError, api } from '$lib/api';
import { clearRoomMark, hasRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { terms } from '$lib/safety/terms.svelte';
import { TERMS_DUE, TERMS_OK } from '$lib/safety/safety.testkit';
import { toasts } from '$lib/toast.svelte';
import RoomBrowser from './RoomBrowser.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn(() => Promise.resolve()) }));
vi.mock('$lib/api', async (original) => ({
	...(await original<typeof import('$lib/api')>()),
	api: { listRooms: vi.fn(), getRoom: vi.fn() }
}));
const room = {
	code: 'KT7P',
	lang: 'en' as const,
	players: 3,
	maxPlayers: 10,
	categoryLabels: ['Name', 'Animal'],
	customCategories: 0,
	rounds: 5,
	timeLimitSeconds: 60
};
beforeEach(() => {
	i18n.set('en');
	profile.setNickname('Ana');
	clearRoomMark('KT7P');
	terms.apply(TERMS_OK);
	toasts.items = [];
	vi.mocked(goto).mockClear();
	vi.mocked(api.listRooms)
		.mockReset()
		.mockResolvedValue({ rooms: [room] });
	vi.mocked(api.getRoom)
		.mockReset()
		.mockResolvedValue({
			...room,
			phase: 'lobby',
			visibility: 'public',
			joinable: true,
			roomInstanceId: '01020304-0506-4708-890a-0b0c0d0e0f10'
		});
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
});

describe('public room browser', () => {
	it('shows occupied players, settings and casual caption; filters game language independently', async () => {
		render(RoomBrowser);
		expect(await screen.findByText('3/10 players')).toBeTruthy();
		expect(screen.getByText('Name · Animal')).toBeTruthy();
		expect(screen.getByText('5 rounds · 60 s')).toBeTruthy();
		expect(screen.getByText('Casual game — lifetime statistics are not recorded')).toBeTruthy();
		expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
		const filters = screen.getByRole('group', { name: 'Game language' });
		await fireEvent.click(filters.querySelectorAll('button')[2]);
		await waitFor(() =>
			expect(api.listRooms).toHaveBeenLastCalledWith(
				'nl',
				expect.objectContaining({ signal: expect.any(AbortSignal) })
			)
		);
		await fireEvent.click(screen.getByRole('button', { name: 'All' }));
		await waitFor(() =>
			expect(api.listRooms).toHaveBeenLastCalledWith(undefined, expect.anything())
		);
		expect(i18n.locale).toBe('en');
	});
	it('preflights, marks and joins a selected room', async () => {
		render(RoomBrowser);
		await fireEvent.click(await screen.findByRole('button', { name: 'Join KT7P' }));
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(api.getRoom).toHaveBeenCalledWith(
			'KT7P',
			expect.objectContaining({ signal: expect.any(AbortSignal) })
		);
		expect(hasRoomMark('KT7P')).toBe(true);
	});
	it('asks for the terms before joining, and joins only once they are accepted', async () => {
		terms.apply(TERMS_DUE);
		render(RoomBrowser);
		await fireEvent.click(await screen.findByRole('button', { name: 'Join KT7P' }));
		await waitFor(() => expect(terms.open).toBe(true));
		expect(goto).not.toHaveBeenCalled();
		expect(hasRoomMark('KT7P')).toBe(false);
		terms.apply(TERMS_OK);
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(hasRoomMark('KT7P')).toBe(true);
	});
	it('a declined gate joins nothing and frees the buttons', async () => {
		terms.apply(TERMS_DUE);
		render(RoomBrowser);
		await fireEvent.click(await screen.findByRole('button', { name: 'Join KT7P' }));
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		await waitFor(() =>
			expect(screen.getByRole('button', { name: 'Join KT7P' }).hasAttribute('disabled')).toBe(false)
		);
		expect(goto).not.toHaveBeenCalled();
	});
	it('never shows player-written text: built-in labels, and a localized count for custom categories', async () => {
		vi.mocked(api.listRooms).mockResolvedValue({
			rooms: [
				{ ...room, code: 'AAAA', customCategories: 1 },
				{ ...room, code: 'BBBB', categoryLabels: [], customCategories: 3 },
				{ ...room, code: 'CCCC', customCategories: 0 }
			]
		});
		render(RoomBrowser);
		await screen.findByRole('button', { name: 'Join AAAA' });
		const chips = screen
			.getAllByTestId('custom-categories')
			.map((c) => c.textContent?.replace(/\s+/g, ' ').trim());
		expect(chips).toEqual(['✏️ 1 custom category', '✏️ 3 custom categories']);
		// a room with only custom categories has no empty label line, and a room without custom ones no chip
		const items = screen.getAllByRole('listitem');
		expect(within(items[1]).queryByText('Name · Animal')).toBeNull();
		expect(within(items[2]).queryByTestId('custom-categories')).toBeNull();
		const chipTexts = () =>
			screen
				.getAllByTestId('custom-categories')
				.map((c) => c.textContent?.replace(/\s+/g, ' ').trim());
		i18n.set('nl');
		await waitFor(() =>
			expect(chipTexts()).toEqual(['✏️ 1 eigen categorie', '✏️ 3 eigen categorieën'])
		);
		i18n.set('es');
		await waitFor(() =>
			expect(chipTexts()).toEqual(['✏️ 1 categoría propia', '✏️ 3 categorías propias'])
		);
	});
	it('sends a visitor without a nickname through the existing gate', async () => {
		profile.setNickname('');
		render(RoomBrowser);
		await fireEvent.click(await screen.findByRole('button', { name: 'Join KT7P' }));
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(hasRoomMark('KT7P')).toBe(false);
	});
	it('handles a room that filled since discovery', async () => {
		vi.mocked(api.getRoom).mockResolvedValue({
			...room,
			phase: 'lobby',
			visibility: 'public',
			roomInstanceId: '01020304-0506-4708-890a-0b0c0d0e0f10',
			players: 10,
			joinable: false
		});
		render(RoomBrowser);
		await fireEvent.click(await screen.findByRole('button', { name: 'Join KT7P' }));
		await waitFor(() => expect(toasts.items).toHaveLength(1));
		expect(goto).not.toHaveBeenCalled();
		expect(hasRoomMark('KT7P')).toBe(false);
	});
	it('offers public creation when empty and refresh after errors', async () => {
		vi.mocked(api.listRooms)
			.mockRejectedValueOnce(new ApiError(503, 'server_busy'))
			.mockResolvedValue({ rooms: [] });
		render(RoomBrowser);
		await waitFor(() =>
			expect(screen.getByRole('button', { name: 'Refresh' }).hasAttribute('disabled')).toBe(false)
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
		expect(await screen.findByText('No games waiting right now')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Create a public game' }).getAttribute('href')).toBe(
			'/?visibility=public'
		);
	});
	it('says that public games are paused, offers a private game instead, and lists them again once they are back', async () => {
		vi.mocked(api.listRooms)
			.mockResolvedValueOnce({ rooms: [], paused: true })
			.mockResolvedValue({ rooms: [room] });
		render(RoomBrowser);
		expect(
			await screen.findByText(
				'Public games are paused for now. You can still play a private game with friends.'
			)
		).toBeTruthy();
		// Not the "empty" state: no invitation to create a public game that would be refused.
		expect(screen.queryByText('No games waiting right now')).toBeNull();
		expect(screen.queryByRole('link', { name: 'Create a public game' })).toBeNull();
		expect(screen.getByRole('link', { name: 'Create game' }).getAttribute('href')).toBe('/');
		i18n.set('es');
		expect(
			await screen.findByText(
				'Las partidas públicas están en pausa por ahora. Podés jugar una partida privada con amigos.'
			)
		).toBeTruthy();
		i18n.set('nl');
		expect(
			await screen.findByText(
				'Openbare spellen zijn even gepauzeerd. Je kunt wel een privéspel met vrienden spelen.'
			)
		).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Vernieuwen' }));
		expect(await screen.findByRole('button', { name: 'Meedoen met KT7P' })).toBeTruthy();
		expect(screen.queryByTestId('rooms-paused')).toBeNull();
	});
});
