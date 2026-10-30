// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ClosedReason } from '$lib/game/session';
import { i18n } from '$lib/i18n.svelte';
import {
	FakeWorker,
	installFakeServiceWorker,
	restoreFakeServiceWorker
} from '$lib/pwa/sw.testkit';
import { RELOAD_STAMP_KEY, resetUpdateForTests } from '$lib/pwa/update.svelte';
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

	describe('unsupported_version: getting the new version', () => {
		let container: ReturnType<typeof installFakeServiceWorker>['container'];
		let reload: ReturnType<typeof installFakeServiceWorker>['reload'];
		let online = true;

		beforeEach(() => {
			resetUpdateForTests();
			sessionStorage.clear();
			online = true;
			vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online);
			({ container, reload } = installFakeServiceWorker());
		});
		afterEach(() => {
			restoreFakeServiceWorker();
			vi.restoreAllMocks();
		});

		const status = () => screen.getByRole('status');
		const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
		const visible = (state: DocumentVisibilityState) => {
			Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
			document.dispatchEvent(new Event('visibilitychange'));
		};

		it('looks for the new version as soon as it shows, and again when the tab is visible or online', async () => {
			render(ClosedScreen, { reason: 'unsupported_version' });
			await vi.waitFor(() => expect(container.registration!.update).toHaveBeenCalledTimes(1));
			visible('hidden');
			await tick();
			expect(container.registration!.update).toHaveBeenCalledTimes(1);
			visible('visible');
			await vi.waitFor(() => expect(container.registration!.update).toHaveBeenCalledTimes(2));
			window.dispatchEvent(new Event('online'));
			await vi.waitFor(() => expect(container.registration!.update).toHaveBeenCalledTimes(3));
		});

		it('the other reasons never look for updates and have no status line', async () => {
			render(ClosedScreen, { reason: 'kicked' });
			window.dispatchEvent(new Event('online'));
			await tick();
			expect(container.registration!.update).not.toHaveBeenCalled();
			expect(screen.queryByRole('status')).toBeNull();
		});

		it('Reload lets the waiting worker take over and only then reloads, once', async () => {
			const worker = new FakeWorker('installed');
			container.registration!.waiting = worker;
			render(ClosedScreen, { reason: 'unsupported_version' });
			await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
			await tick();
			expect(reload, 'not before the new worker is in charge').not.toHaveBeenCalled();
			expect((screen.getByRole('button', { name: 'Recargar' }) as HTMLButtonElement).disabled).toBe(
				true
			);
			worker.moveTo('activated');
			await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce());
			expect(worker.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' });
			expect(status().textContent?.trim()).toBe('');
		});

		it('keeps the room: the per-tab marker is untouched by the reload', async () => {
			sessionStorage.setItem('tf.room.KT7P', '1');
			const worker = new FakeWorker('installed');
			container.registration!.waiting = worker;
			render(ClosedScreen, { reason: 'unsupported_version' });
			await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
			await tick();
			worker.moveTo('activated');
			await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce());
			expect(sessionStorage.getItem('tf.room.KT7P')).toBe('1');
		});

		it('says it is not ready yet instead of reloading into the same screen', async () => {
			render(ClosedScreen, { reason: 'unsupported_version' });
			await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
			await vi.waitFor(() =>
				expect(status().textContent).toContain('La versión nueva todavía no está lista')
			);
			expect(reload).not.toHaveBeenCalled();
			expect((screen.getByRole('button', { name: 'Recargar' }) as HTMLButtonElement).disabled).toBe(
				false
			);
		});

		it('does not reload a second time within 30 s (loop guard) and says so', async () => {
			sessionStorage.setItem(RELOAD_STAMP_KEY, String(Date.now() - 2000));
			container.registration!.waiting = new FakeWorker('installed');
			const worker = container.registration!.waiting;
			render(ClosedScreen, { reason: 'unsupported_version' });
			await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
			await tick();
			worker.moveTo('activated');
			await vi.waitFor(() =>
				expect(status().textContent).toContain('La versión nueva todavía no está lista')
			);
			expect(reload).not.toHaveBeenCalled();
		});

		it('offline: a recoverable message in a polite live region, in every language', async () => {
			online = false;
			render(ClosedScreen, { reason: 'unsupported_version' });
			await vi.waitFor(() => expect(status().textContent).toContain('Estás sin conexión'));
			expect(status().getAttribute('aria-live')).toBe('polite');
			expect(reload).not.toHaveBeenCalled();
			i18n.set('en');
			await vi.waitFor(() => expect(status().textContent).toContain('You are offline'));
			i18n.set('nl');
			await vi.waitFor(() => expect(status().textContent).toContain('Je bent offline'));
			i18n.set('es');
			online = true;
			window.dispatchEvent(new Event('online'));
			await vi.waitFor(() => expect(status().textContent?.trim()).toBe(''));
		});

		it('the actions are real touch targets (>= 48 px)', () => {
			render(ClosedScreen, { reason: 'unsupported_version' });
			const button = screen.getByRole('button', { name: 'Recargar' });
			expect(button.className).toMatch(/\bh-14\b/);
			expect(screen.getByRole('link', { name: 'Ir al inicio' }).className).toMatch(/\bh-14\b/);
		});
	});
});
