// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { ApiError, api } from '$lib/api';
import { clearRoomMark, hasRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
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
	rounds: 5,
	timeLimitSeconds: 60
};
beforeEach(() => {
	i18n.set('en');
	profile.setNickname('Ana');
	clearRoomMark('KT7P');
	toasts.items = [];
	vi.mocked(goto).mockClear();
	vi.mocked(api.listRooms)
		.mockReset()
		.mockResolvedValue({ rooms: [room] });
	vi.mocked(api.getRoom)
		.mockReset()
		.mockResolvedValue({ ...room, phase: 'lobby', visibility: 'public', joinable: true });
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
});
