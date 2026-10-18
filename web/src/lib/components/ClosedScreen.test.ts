// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ClosedReason } from '$lib/game/session';
import { i18n } from '$lib/i18n.svelte';
import ClosedScreen from './ClosedScreen.svelte';

describe('ClosedScreen', () => {
	beforeEach(() => i18n.set('es'));

	const heading = () => screen.getByRole('heading', { level: 1 }).textContent;

	it.each<[ClosedReason, string]>([
		['room_gone', 'La sala ya no existe'],
		['kicked', 'Te sacaron de la sala'],
		['left', 'Saliste de la partida'],
		['replaced', 'Esta sala se abrió en otro lugar'],
		['unsupported_version', 'Nueva versión disponible'],
		['join_rejected', 'No pudiste entrar']
	])('%s explains itself and always offers a way Home', (reason, title) => {
		render(ClosedScreen, { reason, joinError: 'lobby_full' });
		expect(heading()).toBe(title);
		const home = screen.getByRole('link', { name: 'Ir al inicio' });
		expect(home.getAttribute('href')).toBe('/');
	});

	it('replaced: offers to take the seat back', async () => {
		const onretry = vi.fn();
		render(ClosedScreen, { reason: 'replaced', onretry });
		await fireEvent.click(screen.getByRole('button', { name: 'Recuperar mi lugar acá' }));
		expect(onretry).toHaveBeenCalledOnce();
		expect(screen.getByText(/otra pestaña o dispositivo/)).toBeTruthy();
	});

	it('unsupported_version: Reload button reloads the page', async () => {
		const onreload = vi.fn();
		render(ClosedScreen, { reason: 'unsupported_version', onreload });
		await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
		expect(onreload).toHaveBeenCalledOnce();
	});

	it.each([
		['lobby_full', 'La sala está llena.'],
		['invalid_name', 'Ese apodo no es válido.'],
		['wrong_phase', 'Esta partida ya terminó y no se puede entrar.'],
		[null, 'Algo salió mal. Probá de nuevo.']
	])('join_rejected (%s) says why', async (joinError, text) => {
		const onretry = vi.fn();
		render(ClosedScreen, { reason: 'join_rejected', joinError, onretry });
		expect(screen.getByText(text)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Probar de nuevo' }));
		expect(onretry).toHaveBeenCalledOnce();
	});

	it('only shows a second action where it makes sense', () => {
		for (const reason of ['room_gone', 'kicked', 'left'] as const) {
			const { unmount } = render(ClosedScreen, { reason, onretry: () => {} });
			expect(screen.queryAllByRole('button')).toHaveLength(0);
			unmount();
		}
	});

	it('is translated', () => {
		i18n.set('en');
		render(ClosedScreen, { reason: 'kicked' });
		expect(heading()).toBe('You were removed from the room');
		expect(screen.getByRole('link', { name: 'Go home' })).toBeTruthy();
		i18n.set('es');
	});
});
