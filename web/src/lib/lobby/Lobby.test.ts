// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { flushSync, tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { makeSettings } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import type { Settings } from '$lib/protocol';
import Lobby from '$lib/screens/Lobby.svelte';
import { toasts } from '$lib/toast.svelte';
import { catalog } from './catalog.svelte';
import { fixtureFetch, labelsFor } from './fixtures';
import { PENDING_MS } from './sync.svelte';
import { lobbyView, players, ServerLikeSession } from './testing';

function setNavigator(props: Record<string, unknown>) {
	for (const [key, value] of Object.entries(props)) {
		Object.defineProperty(navigator, key, { value, configurable: true });
	}
}

function host(n = 3, settings = makeSettings()) {
	return new ServerLikeSession(
		lobbyView(n, 'p1', { settings, categoryLabels: labelsFor(settings.lang, settings.categories) })
	);
}

function guest(settings = makeSettings({ timeLimitSeconds: 90 })) {
	return new ServerLikeSession(
		lobbyView(3, 'p2', { settings, categoryLabels: labelsFor(settings.lang, settings.categories) })
	);
}

const sent = (session: ServerLikeSession, cmd: string) => session.sent.filter((c) => c.cmd === cmd);

describe('Lobby', () => {
	beforeEach(() => {
		i18n.set('es');
		localStorage.clear();
		toasts.items = [];
		catalog.reset(fixtureFetch());
		setNavigator({ share: undefined, clipboard: undefined });
	});
	afterEach(() => {
		vi.useRealTimers();
		setNavigator({ share: undefined, clipboard: undefined });
	});

	describe('the host', () => {
		it('sees the code, the players, the settings and Start', () => {
			render(Lobby, { session: host() });
			expect(
				screen.getByRole('button', { name: 'Copiar el código K T 7 P' }).textContent
			).toContain('KT7P');
			expect(screen.getByRole('button', { name: 'Compartir' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Mostrar el código QR' })).toBeTruthy();
			expect(screen.getByRole('heading', { name: 'Jugadores' })).toBeTruthy();
			expect(screen.getByLabelText('3 de 10 jugadores').textContent?.trim()).toBe('3/10');
			expect(screen.getByRole('heading', { name: 'Ajustes' })).toBeTruthy();
			expect(screen.getByRole('switch', { name: 'Excluir letras difíciles' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Empezar' }).hasAttribute('disabled')).toBe(false);
			expect(screen.getByRole('button', { name: 'Salir' })).toBeTruthy();
		});

		it('marks the host, you and offline players', () => {
			const session = host();
			session.setView({ ...session.view!, players: players(3, { p3: { connected: false } }) });
			render(Lobby, { session });
			const [ana, , caro] = screen.getAllByRole('listitem').slice(0, 3);
			expect(within(ana).getByRole('img', { name: 'Anfitrión' })).toBeTruthy();
			expect(within(ana).getByText('tú')).toBeTruthy();
			expect(within(caro).getByRole('img', { name: 'Sin conexión' })).toBeTruthy();
		});

		it('starts the game', async () => {
			const session = host();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Empezar' }));
			expect(sent(session, 'startGame')).toHaveLength(1);
		});

		it('cannot start alone and says why', async () => {
			const session = host(1);
			render(Lobby, { session });
			const start = screen.getByRole('button', { name: 'Empezar' });
			expect(start.hasAttribute('disabled')).toBe(true);
			expect(start.getAttribute('aria-describedby')).toBe('lobby-start-hint');
			expect(screen.getByText('Esperando al menos 2 jugadores')).toBeTruthy();
			expect(screen.getByText('Compartí el código para que se sumen')).toBeTruthy();
			await fireEvent.click(start);
			expect(sent(session, 'startGame')).toHaveLength(0);
		});

		it('can start as soon as someone joins', async () => {
			const session = host(1);
			render(Lobby, { session });
			session.setView({ ...session.view!, players: players(2) });
			await tick();
			expect(screen.getByRole('button', { name: 'Empezar' }).hasAttribute('disabled')).toBe(false);
			expect(screen.queryByText('Esperando al menos 2 jugadores')).toBeNull();
			expect(screen.queryByText('Compartí el código para que se sumen')).toBeNull();
		});

		it('shows a calm message when the server refuses to start', async () => {
			const session = host();
			render(Lobby, { session });
			session.fail('not_enough_players', 'start_game');
			await tick();
			expect((await screen.findByRole('alert')).textContent).toContain('No se pudo empezar');
			await fireEvent.click(screen.getByRole('button', { name: 'Empezar' }));
			expect(screen.queryByText(/No se pudo empezar/)).toBeNull();
		});

		it('ignores errors that were there before the screen opened', () => {
			const session = host();
			session.fail('not_enough_players', 'start_game');
			render(Lobby, { session });
			expect(screen.queryByText(/No se pudo empezar/)).toBeNull();
		});

		it('asks twice before kicking someone', async () => {
			const session = host();
			render(Lobby, { session });
			expect(screen.queryByRole('button', { name: 'Sacar a Ana' })).toBeNull();
			await fireEvent.click(screen.getByRole('button', { name: 'Sacar a Beto' }));
			expect(screen.getByText('¿Sacar a Beto de la sala?')).toBeTruthy();
			expect(sent(session, 'kick')).toHaveLength(0);
			expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Cancelar' }));

			await fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }));
			expect(screen.queryByText('¿Sacar a Beto de la sala?')).toBeNull();
			await waitFor(() =>
				expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Sacar a Beto' }))
			);
			expect(sent(session, 'kick')).toHaveLength(0);

			await fireEvent.click(screen.getByRole('button', { name: 'Sacar a Beto' }));
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, sacar a Beto' }));
			expect(session.sent.filter((c) => c.cmd === 'kick')).toEqual([{ cmd: 'kick', args: ['p2'] }]);
			await waitFor(() => expect(screen.queryByText('Beto')).toBeNull());
		});

		it('closes a kick prompt when that player leaves on their own', async () => {
			const session = host();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Sacar a Beto' }));
			session.setView({ ...session.view!, players: players(3).filter((p) => p.id !== 'p2') });
			await tick();
			expect(screen.queryByText('¿Sacar a Beto de la sala?')).toBeNull();
		});

		it('announces who joins and leaves', async () => {
			const session = host();
			render(Lobby, { session });
			const live = screen
				.getAllByRole('status')
				.find((el) => el.getAttribute('aria-live') === 'polite')!;
			expect(live.textContent).toBe('');
			session.setView({ ...session.view!, players: players(4) });
			await tick();
			expect(live.textContent).toBe('Dani se sumó');
			session.setView({ ...session.view!, players: players(4).filter((p) => p.id !== 'p2') });
			await tick();
			expect(live.textContent).toBe('Beto se fue');
		});

		it('leaves only after confirming', async () => {
			const session = host();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Salir' }));
			const dialog = screen.getByRole('dialog', { name: '¿Salir de la sala?' });
			expect(within(dialog).getByText(/Sos el anfitrión/)).toBeTruthy();
			expect(sent(session, 'leave')).toHaveLength(0);
			await fireEvent.click(within(dialog).getByRole('button', { name: 'Quedarme' }));
			expect(screen.queryByRole('dialog')).toBeNull();
			expect(sent(session, 'leave')).toHaveLength(0);

			await fireEvent.click(screen.getByRole('button', { name: 'Salir' }));
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, salir' }));
			expect(sent(session, 'leave')).toHaveLength(1);
		});
	});

	describe('everyone else', () => {
		it('sees the settings read-only, who sets them up and a waiting message', () => {
			const session = guest();
			render(Lobby, { session });
			expect(screen.getByText('Configura: Ana')).toBeTruthy();
			expect(screen.getByText('Esperando a que Ana empiece…')).toBeTruthy();
			expect(screen.getByText('Español')).toBeTruthy();
			expect(screen.getByText('Sin letras difíciles (20 letras)')).toBeTruthy();
			expect(screen.getByText('Rondas: 5')).toBeTruthy();
			expect(screen.getByText('Tiempo: 90 s')).toBeTruthy();
			expect(screen.getByText('6/10')).toBeTruthy();
			expect(screen.getByText('Comida')).toBeTruthy();
			expect(screen.queryByRole('button', { name: 'Empezar' })).toBeNull();
			expect(screen.queryByRole('switch')).toBeNull();
			expect(screen.queryByRole('button', { name: 'Editar categorías' })).toBeNull();
			expect(screen.queryByRole('button', { name: /^Sacar a/ })).toBeNull();
			expect(screen.getByRole('button', { name: 'Salir' })).toBeTruthy();
		});

		it('sees unlimited rounds, no time limit and all letters', () => {
			const settings = makeSettings({ rounds: 0, excludeHardLetters: false, lang: 'en' });
			render(Lobby, { session: guest(settings) });
			expect(screen.getByText('Rondas: sin límite')).toBeTruthy();
			expect(screen.getByText('Tiempo: libre')).toBeTruthy();
			expect(screen.getByText('Todas las letras (26)')).toBeTruthy();
			expect(screen.getByText('Inglés')).toBeTruthy();
			// category labels come from the lobby language, not the UI language
			expect(screen.getByText('Name')).toBeTruthy();
		});

		it('sees whether asking everyone to stop is allowed, read-only', () => {
			const { unmount } = render(Lobby, { session: guest(makeSettings({ stopVote: true })) });
			expect(screen.getByText('Se puede pedir que paren')).toBeTruthy();
			expect(screen.queryByRole('switch')).toBeNull();
			expect(screen.queryByText('Permitir pedir que paren')).toBeNull();
			unmount();
			render(Lobby, { session: guest(makeSettings({ stopVote: false })) });
			expect(screen.getByText('Sin pedir que paren')).toBeTruthy();
			expect(screen.queryByText('Se puede pedir que paren')).toBeNull();
		});

		it('sees it in English and Dutch', () => {
			i18n.set('en');
			const { unmount } = render(Lobby, { session: guest(makeSettings({ stopVote: true })) });
			expect(screen.getByText('Can ask everyone to stop')).toBeTruthy();
			unmount();
			i18n.set('nl');
			render(Lobby, { session: guest(makeSettings({ stopVote: false })) });
			expect(screen.getByText('Vragen om te stoppen uit')).toBeTruthy();
		});

		it('follows the host turning it off', async () => {
			const session = guest();
			render(Lobby, { session });
			session.setView({ ...session.view!, settings: makeSettings({ stopVote: false }) });
			await tick();
			expect(screen.getByText('Sin pedir que paren')).toBeTruthy();
		});

		it('sees a Dutch lobby: language chip, 23 letters and Dutch category labels', () => {
			const settings = makeSettings({ lang: 'nl' });
			render(Lobby, { session: guest(settings) });
			expect(screen.getByText('Holandés')).toBeTruthy();
			expect(screen.getByText('Sin letras difíciles (23 letras)')).toBeTruthy();
			expect(screen.getByText('Naam')).toBeTruthy();
			expect(screen.getByText('Land')).toBeTruthy();
			expect(screen.queryByText('Nombre')).toBeNull();
		});

		it('marks custom categories in the read-only list', () => {
			const settings = makeSettings({
				categories: [{ id: 'nombre' }, { id: 'pais' }, { custom: 'Excusas' }]
			});
			render(Lobby, { session: guest(settings) });
			expect(screen.getByRole('img', { name: 'Categoría propia' })).toBeTruthy();
			expect(screen.getByText('Excusas')).toBeTruthy();
		});

		it('follows the host changing settings', async () => {
			const session = guest();
			render(Lobby, { session });
			session.setView({
				...session.view!,
				settings: makeSettings({ rounds: 9 })
			});
			await tick();
			expect(screen.getByText('Rondas: 9')).toBeTruthy();
		});

		it('leaves without the host warning', async () => {
			const session = guest();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Salir' }));
			expect(screen.getByText('Vas a dejar tu lugar en esta sala.')).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, salir' }));
			expect(sent(session, 'leave')).toHaveLength(1);
		});

		it('becomes the host when the server says so', async () => {
			const session = guest();
			render(Lobby, { session });
			session.setView({ ...session.view!, hostId: 'p2' });
			await tick();
			expect(screen.getByRole('switch', { name: 'Excluir letras difíciles' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Empezar' })).toBeTruthy();
		});
	});

	describe('invite', () => {
		it('copies the code with a tap and says so', async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			setNavigator({ clipboard: { writeText } });
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Copiar el código K T 7 P' }));
			await waitFor(() => expect(toasts.items.map((t) => t.text)).toEqual(['Código copiado']));
			expect(writeText).toHaveBeenCalledWith('KT7P');
		});

		it('tells when copying is impossible', async () => {
			document.execCommand = vi.fn().mockReturnValue(false);
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Copiar el código K T 7 P' }));
			await waitFor(() => expect(toasts.items.map((t) => t.text)).toEqual(['No se pudo copiar']));
		});

		it('shares with the Web Share API', async () => {
			const share = vi.fn().mockResolvedValue(undefined);
			setNavigator({ share });
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Compartir' }));
			await waitFor(() => expect(share).toHaveBeenCalledTimes(1));
			expect(share).toHaveBeenCalledWith({
				title: 'Tutti Frutti',
				text: '¡Jugá Tutti Frutti conmigo! Código KT7P',
				url: `${location.origin}/KT7P`
			});
			expect(toasts.items).toHaveLength(0);
		});

		it('copies the invite when the share fails, but not when the person cancels', async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			setNavigator({
				clipboard: { writeText },
				share: vi.fn().mockRejectedValue(new DOMException('closed', 'AbortError'))
			});
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Compartir' }));
			await tick();
			expect(writeText).not.toHaveBeenCalled();

			setNavigator({ share: vi.fn().mockRejectedValue(new Error('NotAllowedError')) });
			await fireEvent.click(screen.getByRole('button', { name: 'Compartir' }));
			await waitFor(() => expect(toasts.items.map((t) => t.text)).toEqual(['Invitación copiada']));
			expect(writeText).toHaveBeenCalledWith(
				`¡Jugá Tutti Frutti conmigo! Código KT7P ${location.origin}/KT7P`
			);
		});

		it('falls back to copying without the Web Share API', async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			setNavigator({ clipboard: { writeText } });
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Compartir' }));
			await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
			await waitFor(() => expect(toasts.items).toHaveLength(1));
		});

		it('reports when neither sharing nor copying works', async () => {
			document.execCommand = vi.fn().mockReturnValue(false);
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Compartir' }));
			await waitFor(() => expect(toasts.items.map((t) => t.text)).toEqual(['No se pudo copiar']));
		});

		it('opens a QR dialog with the invite link and closes it again', async () => {
			render(Lobby, { session: host() });
			const opener = screen.getByRole('button', { name: 'Mostrar el código QR' });
			opener.focus();
			await fireEvent.click(opener);
			const dialog = screen.getByRole('dialog', { name: 'Invitá con un QR' });
			expect(dialog.getAttribute('aria-modal')).toBe('true');
			expect(within(dialog).getByText('Escaneá para unirte')).toBeTruthy();
			expect(within(dialog).getByText(`${location.origin}/KT7P`)).toBeTruthy();
			const qr = await within(dialog).findByRole('img', {
				name: 'Código QR para unirse a la sala KT7P'
			});
			expect(qr.querySelector('path')?.getAttribute('d')?.length).toBeGreaterThan(100);
			expect(document.body.style.overflow).toBe('hidden');
			// the dialog takes focus (the close ✕ first) ...
			expect(dialog.contains(document.activeElement)).toBe(true);

			// ... and keeps it: Tab wraps around inside it
			const buttons = within(dialog).getAllByRole('button');
			buttons[buttons.length - 1].focus();
			await fireEvent.keyDown(buttons[buttons.length - 1], { key: 'Tab' });
			expect(document.activeElement).toBe(buttons[0]);
			await fireEvent.keyDown(buttons[0], { key: 'Tab', shiftKey: true });
			expect(document.activeElement).toBe(buttons[buttons.length - 1]);

			await fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
			expect(screen.queryByRole('dialog')).toBeNull();
			expect(document.body.style.overflow).toBe('');
			expect(document.activeElement).toBe(opener);
		});

		it('brings focus back into the dialog when Tab is pressed outside it', async () => {
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Mostrar el código QR' }));
			const dialog = screen.getByRole('dialog');
			(document.activeElement as HTMLElement).blur();
			await fireEvent.keyDown(window, { key: 'Tab' });
			expect(dialog.contains(document.activeElement)).toBe(true);
			// Shift+Tab on the dialog itself wraps to the last control
			dialog.focus();
			await fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
			const buttons = within(dialog).getAllByRole('button');
			expect(document.activeElement).toBe(buttons[buttons.length - 1]);
		});

		it('closes the QR dialog with its button and by tapping outside', async () => {
			render(Lobby, { session: host() });
			await fireEvent.click(screen.getByRole('button', { name: 'Mostrar el código QR' }));
			const dialog = screen.getByRole('dialog');
			await fireEvent.click(within(dialog).getAllByRole('button', { name: 'Cerrar' })[1]);
			expect(screen.queryByRole('dialog')).toBeNull();

			await fireEvent.click(screen.getByRole('button', { name: 'Mostrar el código QR' }));
			await fireEvent.click(screen.getByRole('dialog'));
			expect(screen.queryByRole('dialog')).not.toBeNull();
			await fireEvent.click(screen.getByRole('dialog').parentElement!);
			expect(screen.queryByRole('dialog')).toBeNull();
		});
	});

	describe('settings (host)', () => {
		const updates = (session: ServerLikeSession) => sent(session, 'updateSettings');

		it('changes the lobby language and clamps the rounds', async () => {
			const session = host(3, makeSettings({ rounds: 20 }));
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Inglés' }));
			expect(updates(session)).toEqual([
				{ cmd: 'updateSettings', args: [makeSettings({ rounds: 20, lang: 'en' })] }
			]);
			await waitFor(() => expect(screen.getByText(/Out: |Fuera: Q U V X Y Z/)).toBeTruthy());
		});

		it('offers the three lobby languages and marks the current one', () => {
			render(Lobby, { session: host(3, makeSettings({ lang: 'nl' })) });
			const group = screen.getByRole('group', { name: 'Idioma de la partida' });
			const buttons = within(group).getAllByRole('button');
			expect(buttons.map((b) => b.textContent?.trim())).toEqual(['Español', 'Inglés', 'Holandés']);
			expect(buttons.map((b) => b.getAttribute('aria-pressed'))).toEqual([
				'false',
				'false',
				'true'
			]);
		});

		it('changes the lobby language to Dutch and clamps the rounds to its 26 letters', async () => {
			const session = host(3, makeSettings({ excludeHardLetters: false, rounds: 27 }));
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Holandés' }));
			expect(updates(session)).toEqual([
				{
					cmd: 'updateSettings',
					args: [makeSettings({ excludeHardLetters: false, rounds: 26, lang: 'nl' })]
				}
			]);
			await waitFor(() => expect(screen.getByText('Se juegan las 26 letras')).toBeTruthy());
			expect(screen.getByText('hasta 26')).toBeTruthy();
			// the picker follows the lobby language
			await waitFor(() => expect(screen.getByText('Naam')).toBeTruthy());
		});

		it('shows the Dutch hard letters (Q X Y) and 23 playable letters', async () => {
			const session = host(3, makeSettings({ lang: 'nl', rounds: 23 }));
			render(Lobby, { session });
			expect(screen.getByText('Fuera: Q X Y · se juegan 23 letras')).toBeTruthy();
			expect(screen.getByText('hasta 23')).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Una ronda más' }).hasAttribute('disabled')).toBe(
				true
			);
			await fireEvent.click(screen.getByRole('switch', { name: 'Excluir letras difíciles' }));
			expect(updates(session)[0].args[0]).toEqual(
				makeSettings({ lang: 'nl', rounds: 23, excludeHardLetters: false })
			);
			await waitFor(() => expect(screen.getByText('Se juegan las 26 letras')).toBeTruthy());
			expect(screen.getByText('hasta 26')).toBeTruthy();
		});

		it('toggles the hard letters and shows the real ones for the language', async () => {
			const session = host(3, makeSettings({ excludeHardLetters: false, rounds: 25 }));
			render(Lobby, { session });
			expect(screen.getByText('Se juegan las 27 letras')).toBeTruthy();
			await fireEvent.click(screen.getByRole('switch', { name: 'Excluir letras difíciles' }));
			expect(updates(session)[0].args[0]).toEqual(
				makeSettings({ excludeHardLetters: true, rounds: 20 })
			);
			await waitFor(() =>
				expect(screen.getByText('Fuera: K Ñ Q W X Y Z · se juegan 20 letras')).toBeTruthy()
			);
			expect(
				screen
					.getByRole('switch', { name: 'Excluir letras difíciles' })
					.getAttribute('aria-checked')
			).toBe('true');
		});

		it('has a switch to allow asking everyone to stop, on by default, with its hint', () => {
			render(Lobby, { session: host() });
			const toggle = screen.getByRole('switch', { name: 'Permitir pedir que paren' });
			expect(toggle.getAttribute('aria-checked')).toBe('true');
			expect(toggle.className).toContain('min-h-14');
			expect(toggle.getAttribute('aria-describedby')).toBe('stop-desc');
			expect(document.getElementById('stop-desc')?.textContent?.trim()).toBe(
				'Quien no terminó puede pedir que paren; todos tienen que aceptar.'
			);
		});

		it('turns the vote to stop off and on again, sending the WHOLE settings each time', async () => {
			const settings = makeSettings({ lang: 'nl', rounds: 7, timeLimitSeconds: 90 });
			const session = host(3, settings);
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('switch', { name: 'Permitir pedir que paren' }));
			expect(updates(session)).toEqual([
				{ cmd: 'updateSettings', args: [{ ...settings, stopVote: false }] }
			]);
			await waitFor(() =>
				expect(
					screen
						.getByRole('switch', { name: 'Permitir pedir que paren' })
						.getAttribute('aria-checked')
				).toBe('false')
			);
			expect(screen.getByText('Solo se puede cantar ¡BASTA! con todo completo.')).toBeTruthy();
			await fireEvent.click(screen.getByRole('switch', { name: 'Permitir pedir que paren' }));
			expect(updates(session)[1].args[0]).toEqual(settings);
		});

		it('keeps the vote to stop when another setting changes', async () => {
			const session = host(3, makeSettings({ stopVote: false, rounds: 7 }));
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			expect(updates(session)[0].args[0]).toEqual(makeSettings({ stopVote: false, rounds: 8 }));
			await fireEvent.click(screen.getByRole('button', { name: 'Inglés' }));
			expect((updates(session)[1].args[0] as Settings).stopVote).toBe(false);
		});

		it('says it in English and Dutch without any Spanish', () => {
			i18n.set('en');
			const { unmount } = render(Lobby, { session: host() });
			expect(screen.getByRole('switch', { name: 'Allow asking everyone to stop' })).toBeTruthy();
			expect(document.body.textContent).not.toMatch(/basta/i);
			unmount();
			i18n.set('nl');
			render(Lobby, { session: host(3, makeSettings({ stopVote: false })) });
			expect(screen.getByRole('switch', { name: 'Vragen om te stoppen toestaan' })).toBeTruthy();
			expect(screen.getByText('Stop! kan alleen als alles is ingevuld.')).toBeTruthy();
			expect(document.body.textContent).not.toMatch(/basta/i);
		});

		it('shows the English hard letters', () => {
			render(Lobby, { session: host(3, makeSettings({ lang: 'en' })) });
			expect(screen.getByText('Fuera: Q U V X Y Z · se juegan 20 letras')).toBeTruthy();
		});

		it('steps the rounds within 1..max', async () => {
			const session = host(3, makeSettings({ rounds: 1 }));
			render(Lobby, { session });
			expect(screen.getByRole('button', { name: 'Una ronda menos' }).hasAttribute('disabled')).toBe(
				true
			);
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			expect(updates(session)[0].args[0]).toEqual(makeSettings({ rounds: 2 }));
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			expect(updates(session)[1].args[0]).toEqual(makeSettings({ rounds: 3 }));
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda menos' }));
			expect(updates(session)[2].args[0]).toEqual(makeSettings({ rounds: 2 }));
		});

		it('cannot go above the playable letters', () => {
			render(Lobby, { session: host(3, makeSettings({ rounds: 20 })) });
			expect(screen.getByText('hasta 20')).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Una ronda más' }).hasAttribute('disabled')).toBe(
				true
			);
		});

		it('switches to unlimited rounds and back to the previous number', async () => {
			const session = host(3, makeSettings({ rounds: 7 }));
			render(Lobby, { session });
			const unlimited = screen.getByRole('button', { name: 'Sin límite' });
			expect(unlimited.getAttribute('aria-pressed')).toBe('false');
			await fireEvent.click(unlimited);
			expect(updates(session)[0].args[0]).toEqual(makeSettings({ rounds: 0 }));
			await waitFor(() => expect(unlimited.getAttribute('aria-pressed')).toBe('true'));
			expect(screen.getByRole('button', { name: 'Una ronda más' }).hasAttribute('disabled')).toBe(
				true
			);
			await fireEvent.click(unlimited);
			expect(updates(session)[1].args[0]).toEqual(makeSettings({ rounds: 7 }));
		});

		it('leaves unlimited with 5 rounds when it was unlimited from the start', async () => {
			const session = host(3, makeSettings({ rounds: 0 }));
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Sin límite' }));
			expect(updates(session)[0].args[0]).toEqual(makeSettings({ rounds: 5 }));
		});

		it('sets the time limit', async () => {
			const session = host();
			render(Lobby, { session });
			expect(
				screen.getByRole('button', { name: 'Sin límite de tiempo' }).getAttribute('aria-pressed')
			).toBe('true');
			for (const [name, seconds] of [
				['60 segundos', 60],
				['90 segundos', 90],
				['120 segundos', 120],
				['180 segundos', 180]
			] as const) {
				await fireEvent.click(screen.getByRole('button', { name }));
				expect(updates(session).at(-1)!.args[0]).toEqual(
					makeSettings({ timeLimitSeconds: seconds })
				);
			}
		});

		it('does not send anything when nothing changes', async () => {
			const session = host();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Español' }));
			await fireEvent.click(screen.getByRole('button', { name: 'Sin límite de tiempo' }));
			expect(updates(session)).toHaveLength(0);
		});

		it('previews the categories and opens the picker, which closes with Done, ✕ and Escape', async () => {
			const session = host();
			render(Lobby, { session });
			expect(screen.getByText('6/10')).toBeTruthy();
			const edit = screen.getByRole('button', { name: 'Editar categorías' });
			edit.focus();
			await fireEvent.click(edit);
			const dialog = screen.getByRole('dialog', { name: 'Categorías' });
			expect(within(dialog).getByText('6/10 seleccionadas')).toBeTruthy();
			await fireEvent.click(within(dialog).getByRole('button', { name: 'Listo' }));
			expect(screen.queryByRole('dialog')).toBeNull();

			await fireEvent.click(edit);
			await fireEvent.click(screen.getAllByRole('button', { name: 'Cerrar' })[0]);
			expect(screen.queryByRole('dialog')).toBeNull();

			await fireEvent.click(edit);
			await fireEvent.keyDown(window, { key: 'Escape' });
			expect(screen.queryByRole('dialog')).toBeNull();
			expect(document.activeElement).toBe(edit);
		});

		it('sends the category changes made in the picker', async () => {
			const session = host();
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Editar categorías' }));
			await fireEvent.click(screen.getByRole('button', { name: 'Subir País' }));
			expect(updates(session)[0].args[0]).toEqual(
				makeSettings({
					categories: ['pais', 'nombre', 'animal', 'comida', 'color', 'marca'].map((id) => ({ id }))
				})
			);
			await waitFor(() => expect(screen.getAllByText('País').length).toBeGreaterThan(0));
		});

		it('applies a change at once, builds the next one on it and converges to the server', async () => {
			vi.useFakeTimers();
			const session = host();
			session.options.latencyMs = 500;
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			// not confirmed yet: shown optimistically, and the second edit started from the first
			expect(updates(session).map((c) => (c.args[0] as Settings).rounds)).toEqual([6, 7]);
			expect(screen.getByLabelText('7 rondas')).toBeTruthy();
			await vi.advanceTimersByTimeAsync(600);
			flushSync();
			expect(session.view!.settings.rounds).toBe(7);
			expect(screen.getByLabelText('7 rondas')).toBeTruthy();
		});

		it('goes back to the server value when nothing confirms the edit', async () => {
			vi.useFakeTimers();
			const session = host();
			session.options.unresponsive = true;
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			expect(screen.getByLabelText('6 rondas')).toBeTruthy();
			await vi.advanceTimersByTimeAsync(PENDING_MS + 10);
			flushSync();
			expect(screen.getByLabelText('5 rondas')).toBeTruthy();
		});

		it('shows a calm message and the server value when the server refuses a change', async () => {
			const session = host();
			session.options.rejectSettings = true;
			render(Lobby, { session });
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			const alert = await screen.findByText(/No se pudo aplicar ese cambio/);
			expect(alert.getAttribute('role') ?? alert.closest('[role=alert]')).toBeTruthy();
			expect(screen.getByLabelText('5 rondas')).toBeTruthy();
			session.options.rejectSettings = false;
			await fireEvent.click(screen.getByRole('button', { name: 'Una ronda más' }));
			expect(screen.queryByText(/No se pudo aplicar ese cambio/)).toBeNull();
		});

		it('ignores errors of other commands', async () => {
			const session = host();
			render(Lobby, { session });
			session.fail('invalid_settings', 'kick');
			await tick();
			expect(screen.queryByText(/No se pudo aplicar ese cambio/)).toBeNull();
		});
	});

	describe('English UI', () => {
		it('has no Spanish chrome', () => {
			i18n.set('en');
			render(Lobby, { session: host() });
			expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Leave' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Share' })).toBeTruthy();
			expect(screen.getByRole('heading', { name: 'Players' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Edit categories' })).toBeTruthy();
			expect(screen.getByRole('switch', { name: 'Skip hard letters' })).toBeTruthy();
		});

		it('says Waiting for the host', () => {
			i18n.set('en');
			render(Lobby, { session: guest() });
			expect(screen.getByText('Waiting for Ana to start…')).toBeTruthy();
			expect(screen.getByText('Set up by Ana')).toBeTruthy();
		});
	});

	describe('Dutch UI', () => {
		it('has no Spanish or English chrome', () => {
			i18n.set('nl');
			render(Lobby, { session: host() });
			expect(screen.getByRole('button', { name: 'Starten' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Verlaten' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Delen' })).toBeTruthy();
			expect(screen.getByRole('heading', { name: 'Spelers' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Categorieën aanpassen' })).toBeTruthy();
			expect(screen.getByRole('switch', { name: 'Moeilijke letters overslaan' })).toBeTruthy();
		});

		it('names the three game languages in Dutch, independent of the Spanish lobby', () => {
			i18n.set('nl');
			render(Lobby, { session: host() });
			const group = screen.getByRole('group', { name: 'Taal van het spel' });
			const buttons = within(group).getAllByRole('button');
			expect(buttons.map((b) => b.textContent?.trim())).toEqual(['Spaans', 'Engels', 'Nederlands']);
			// the lobby is Spanish (es) while the screen is Dutch
			expect(buttons[0].getAttribute('aria-pressed')).toBe('true');
			// Spanish category labels are shown as is: they belong to the lobby language
			expect(screen.getAllByText('Nombre').length).toBeGreaterThan(0);
		});

		it('shows a Dutch lobby on a Dutch screen: "Nederlands", Dutch letters and category labels', () => {
			i18n.set('nl');
			render(Lobby, { session: guest(makeSettings({ lang: 'nl', timeLimitSeconds: 90 })) });
			expect(screen.getByText('Nederlands')).toBeTruthy();
			expect(screen.getByText('Geen moeilijke letters (23 letters)')).toBeTruthy();
			expect(screen.getByText('Tijd: 90 s')).toBeTruthy();
			expect(screen.getByText('Wachten tot Ana start…')).toBeTruthy();
			expect(screen.getByText('Eten')).toBeTruthy();
		});
	});

	it('renders nothing until the first state arrives', () => {
		const session = new ServerLikeSession(null);
		render(Lobby, { session });
		expect(screen.queryByRole('main')).toBeNull();
	});
});
