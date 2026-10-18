// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type RoomInfo } from '$lib/api';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import JoinGate from './JoinGate.svelte';

const typeInto = (input: HTMLElement, value: string) =>
	fireEvent.input(input, { target: { value } });

const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 3,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	...over
});

function gate(load: (code: string) => Promise<RoomInfo>, onjoin = vi.fn()) {
	render(JoinGate, { code: 'KT7P', onjoin, load });
	return onjoin;
}

describe('JoinGate', () => {
	beforeEach(() => {
		i18n.set('es');
		profile.setNickname('Ana');
	});

	it('checks the room first, then shows code, players, nickname, avatar and Join', async () => {
		const load = vi.fn(() => Promise.resolve(info()));
		gate(load);
		expect(load).toHaveBeenCalledWith('KT7P');
		expect(await screen.findByText('3 de 10 jugadores')).toBeTruthy();
		expect(screen.getByLabelText('Sala KT7P').textContent?.trim()).toBe('KT7P');
		expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Unite a la sala');
		expect((screen.getByLabelText('Tu apodo') as HTMLInputElement).value).toBe('Ana');
		expect(screen.getByRole('group', { name: 'Elegí un emoji' })).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Ir al inicio' }).getAttribute('href')).toBe('/');
	});

	it('Join is disabled while checking and while there is no nickname', async () => {
		let resolveInfo!: (i: RoomInfo) => void;
		gate(() => new Promise((r) => (resolveInfo = r)));
		const join = () =>
			screen.getByRole('button', { name: 'Entrar a la sala' }) as HTMLButtonElement;
		expect(screen.getByText('Buscando la sala…')).toBeTruthy();
		expect(join().disabled).toBe(true);
		resolveInfo(info());
		await waitFor(() => expect(join().disabled).toBe(false));

		const input = screen.getByLabelText('Tu apodo') as HTMLInputElement;
		await typeInto(input, '');
		expect(profile.nickname).toBe('');
		expect(join().disabled).toBe(true);
		expect(screen.getByText('Elegí un apodo para entrar')).toBeTruthy();
	});

	it('pressing Join (or Enter in the nickname field) calls onjoin once the nickname is valid', async () => {
		const onjoin = gate(() => Promise.resolve(info()));
		await screen.findByText('3 de 10 jugadores');
		await fireEvent.click(screen.getByRole('button', { name: 'Entrar a la sala' }));
		expect(onjoin).toHaveBeenCalledTimes(1);
		const input = screen.getByLabelText('Tu apodo');
		await typeInto(input, 'Anaita');
		await fireEvent.keyDown(input, { key: 'Enter' });
		expect(profile.nickname).toBe('Anaita');
		expect(onjoin).toHaveBeenCalledTimes(2);
	});

	it('caps the nickname at 16 characters like Home', async () => {
		profile.setNickname('');
		gate(() => Promise.resolve(info()));
		await screen.findByText('3 de 10 jugadores');
		await typeInto(screen.getByLabelText('Tu apodo'), 'abcdefghijklmnopqrstuvwxyz');
		expect(profile.nickname).toBe('abcdefghijklmnop');
	});

	it('changes the avatar through the shared picker', async () => {
		gate(() => Promise.resolve(info()));
		await screen.findByText('3 de 10 jugadores');
		await fireEvent.click(screen.getByRole('button', { name: '🍓' }));
		expect(profile.avatar.emoji).toBe('🍓');
	});

	it('404: "no existe esa sala" with only a Home button', async () => {
		gate(() => Promise.reject(new ApiError(404, 'room_not_found')));
		expect(await screen.findByRole('heading', { name: 'No existe esa sala' })).toBeTruthy();
		expect(screen.queryByRole('button')).toBeNull();
		expect(screen.getByRole('link', { name: 'Ir al inicio' })).toBeTruthy();
	});

	it('full room: says so, Home, and a way in for a returning player', async () => {
		const onjoin = gate(() => Promise.resolve(info({ players: 10, joinable: false })));
		expect(await screen.findByRole('heading', { name: 'La sala está llena' })).toBeTruthy();
		expect(screen.getByText('Esta sala ya tiene 10 jugadores.')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Ir al inicio' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Ya estaba en esta sala' }));
		expect(onjoin).toHaveBeenCalledOnce();
	});

	it('finished game: says so with Home', async () => {
		gate(() => Promise.resolve(info({ phase: 'final', joinable: false })));
		expect(await screen.findByRole('heading', { name: 'La partida terminó' })).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Ir al inicio' })).toBeTruthy();
	});

	it('hides "I was already here" when there is no nickname to rejoin with', async () => {
		profile.setNickname('');
		gate(() => Promise.resolve(info({ players: 10, joinable: false })));
		await screen.findByRole('heading', { name: 'La sala está llena' });
		expect(screen.queryByRole('button', { name: 'Ya estaba en esta sala' })).toBeNull();
	});

	it('server busy / network: offers Try again, which asks again', async () => {
		const load = vi
			.fn<(code: string) => Promise<RoomInfo>>()
			.mockRejectedValueOnce(new ApiError(503, 'server_busy'))
			.mockResolvedValueOnce(info());
		gate(load);
		expect(await screen.findByRole('heading', { name: 'No pudimos conectar' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }));
		expect(await screen.findByText('3 de 10 jugadores')).toBeTruthy();
		expect(load).toHaveBeenCalledTimes(2);
	});

	it('not joinable although there is room (server draining) is treated as unavailable', async () => {
		gate(() => Promise.resolve(info({ players: 2, joinable: false })));
		expect(await screen.findByRole('heading', { name: 'No pudimos conectar' })).toBeTruthy();
	});

	it('is translated', async () => {
		i18n.set('en');
		gate(() => Promise.resolve(info()));
		expect(await screen.findByText('3 of 10 players')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Join the room' })).toBeTruthy();
		i18n.set('es');
	});
});
