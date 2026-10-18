// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { ApiError, api, type RoomInfo } from '$lib/api';
import { hasRoomMark, clearRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { toasts } from '$lib/toast.svelte';
import Home from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn(() => Promise.resolve()) }));
vi.mock('$lib/api', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api')>()),
	api: { createRoom: vi.fn(), getRoom: vi.fn(), ensureSession: vi.fn() }
}));

const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	...over
});

const create = () => screen.getByRole('button', { name: /Creá una partida|Creando/ });
const join = () => screen.getByRole('button', { name: /Unite a una partida|Entrando/ });
const toastTexts = () => toasts.items.map((t) => t.text);

async function typeCode(code: string) {
	await fireEvent.input(screen.getByLabelText('Carácter 1 de 4'), { target: { value: code } });
}

function deferred<T>() {
	let resolve!: (v: T) => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

describe('Home actions', () => {
	beforeEach(() => {
		i18n.set('es');
		profile.setNickname('Ana');
		profile.setAvatar({ emoji: '🍓', color: 'strawberry' });
		toasts.items = [];
		clearRoomMark('KT7P');
		vi.mocked(goto).mockClear();
		vi.mocked(api.createRoom).mockReset();
		vi.mocked(api.getRoom).mockReset();
	});

	describe('create', () => {
		it('is disabled until there is a nickname', async () => {
			profile.setNickname('');
			render(Home);
			expect((create() as HTMLButtonElement).disabled).toBe(true);
			await fireEvent.input(screen.getByLabelText(/Tu apodo/), { target: { value: 'Beto' } });
			expect((create() as HTMLButtonElement).disabled).toBe(false);
		});

		it('creates the room with nickname, avatar and the UI language, marks the tab and navigates', async () => {
			vi.mocked(api.createRoom).mockResolvedValue({ code: 'KT7P' });
			i18n.set('en');
			render(Home);
			await fireEvent.click(screen.getByRole('button', { name: 'Create game' }));
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
			expect(api.createRoom).toHaveBeenCalledWith({
				name: 'Ana',
				avatar: { emoji: '🍓', color: 'strawberry' },
				lang: 'en'
			});
			expect(hasRoomMark('KT7P')).toBe(true);
			i18n.set('es');
		});

		it('shows progress on the pressed button and disables the other actions meanwhile', async () => {
			const pending = deferred<{ code: string }>();
			vi.mocked(api.createRoom).mockReturnValue(pending.promise);
			render(Home);
			await typeCode('KT7P');
			await fireEvent.click(create());
			await waitFor(() => expect(create().textContent).toContain('Creando…'));
			expect(create().getAttribute('aria-busy')).toBe('true');
			expect((join() as HTMLButtonElement).disabled).toBe(true);
			for (const box of screen.getAllByLabelText(/Carácter \d de 4/)) {
				expect((box as HTMLInputElement).disabled).toBe(true);
			}
			await fireEvent.click(create()); // a second tap does nothing
			expect(api.createRoom).toHaveBeenCalledOnce();
			pending.resolve({ code: 'KT7P' });
			await waitFor(() => expect(goto).toHaveBeenCalledOnce());
			await waitFor(() => expect(create().textContent).toContain('Creá una partida'));
		});

		it.each([
			[new ApiError(503, 'server_busy'), 'El servidor está ocupado. Probá de nuevo en un rato.'],
			[new ApiError(429, 'rate_limited'), 'Vas muy rápido. Esperá un momento.'],
			[new ApiError(400, 'invalid_name'), 'Ese apodo no es válido.'],
			[new ApiError(400, 'invalid_avatar'), 'Ese avatar no es válido.'],
			[new ApiError(403, 'forbidden_origin'), 'Este sitio no está autorizado a conectarse.'],
			[new ApiError(0, 'network'), 'No hay conexión con el servidor.'],
			[new ApiError(418, 'unknown'), 'Algo salió mal. Probá de nuevo.'],
			[new Error('boom'), 'Algo salió mal. Probá de nuevo.']
		])('maps %s to a toast and re-enables the buttons', async (error, text) => {
			vi.mocked(api.createRoom).mockRejectedValue(error);
			render(Home);
			await fireEvent.click(create());
			await waitFor(() => expect(toastTexts()).toEqual([text]));
			expect(goto).not.toHaveBeenCalled();
			expect((create() as HTMLButtonElement).disabled).toBe(false);
			expect(create().getAttribute('aria-busy')).toBe('false');
		});
	});

	describe('join', () => {
		it('needs a complete code and a nickname', async () => {
			render(Home);
			expect((join() as HTMLButtonElement).disabled).toBe(true);
			await typeCode('KT7');
			expect((join() as HTMLButtonElement).disabled).toBe(true);
			await typeCode('KT7P');
			expect((join() as HTMLButtonElement).disabled).toBe(false);
		});

		it('looks the room up first, then marks the tab and navigates', async () => {
			vi.mocked(api.getRoom).mockResolvedValue(info());
			render(Home);
			await typeCode('kt7p');
			await fireEvent.click(join());
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
			expect(api.getRoom).toHaveBeenCalledWith('KT7P');
			expect(api.createRoom).not.toHaveBeenCalled();
			expect(hasRoomMark('KT7P')).toBe(true);
		});

		it('shows progress and disables Create meanwhile', async () => {
			const pending = deferred<RoomInfo>();
			vi.mocked(api.getRoom).mockReturnValue(pending.promise);
			render(Home);
			await typeCode('KT7P');
			await fireEvent.click(join());
			await waitFor(() => expect(join().textContent).toContain('Entrando…'));
			expect(join().getAttribute('aria-busy')).toBe('true');
			expect((create() as HTMLButtonElement).disabled).toBe(true);
			pending.resolve(info());
			await waitFor(() => expect(goto).toHaveBeenCalledOnce());
		});

		it('404: "no existe esa sala" and nothing else happens', async () => {
			vi.mocked(api.getRoom).mockRejectedValue(new ApiError(404, 'room_not_found'));
			render(Home);
			await typeCode('KT7P');
			await fireEvent.click(join());
			await waitFor(() => expect(toastTexts()).toEqual(['No existe esa sala.']));
			expect(goto).not.toHaveBeenCalled();
			expect(hasRoomMark('KT7P')).toBe(false);
			expect((join() as HTMLButtonElement).disabled).toBe(false);
		});

		it.each([
			[info({ players: 10, joinable: false }), 'La sala está llena'],
			[info({ phase: 'final', joinable: false }), 'Esa partida ya terminó'],
			[info({ players: 3, joinable: false }), 'No se puede entrar a esa sala ahora']
		])('not joinable (%o) toasts "%s" and stays on Home', async (room, text) => {
			vi.mocked(api.getRoom).mockResolvedValue(room);
			render(Home);
			await typeCode('KT7P');
			await fireEvent.click(join());
			await waitFor(() => expect(toastTexts()).toEqual([text]));
			expect(goto).not.toHaveBeenCalled();
			expect(hasRoomMark('KT7P')).toBe(false);
		});

		it('network and busy errors become translated toasts', async () => {
			vi.mocked(api.getRoom).mockRejectedValue(new ApiError(503, 'server_busy'));
			render(Home);
			await typeCode('KT7P');
			await fireEvent.click(join());
			await waitFor(() =>
				expect(toastTexts()).toEqual(['El servidor está ocupado. Probá de nuevo en un rato.'])
			);
		});
	});

	it('the Google login stays "coming soon"', async () => {
		render(Home);
		await fireEvent.click(screen.getByRole('link', { name: 'Ingresá con Google' }));
		expect(toastTexts()).toEqual(['Próximamente']);
	});
});
