// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { account, type Outcome } from '$lib/account';
import type { FakeAccount } from '$lib/account/fake.svelte';
import { i18n } from '$lib/i18n.svelte';
import { toasts } from '$lib/toast.svelte';
import Stats from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn(() => Promise.resolve()) }));
vi.mock('$lib/account', async (importOriginal) => {
	const { FakeAccount } = await import('$lib/account/fake.svelte');
	return { ...(await importOriginal<typeof import('$lib/account')>()), account: new FakeAccount() };
});

const fake = account as unknown as FakeAccount;
const toastTexts = () => toasts.items.map((t) => t.text);
const DATA = { gamesPlayed: 12, wins: 5, totalPoints: 1240, bestRound: 80, uniqueAnswers: 37 };
const NONE = { gamesPlayed: 0, wins: 0, totalPoints: 0, bestRound: 0, uniqueAnswers: 0 };

function deferred<T>() {
	let resolve!: (v: T) => void;
	const promise = new Promise<T>((res) => (resolve = res));
	return { promise, resolve };
}

function ready(stats = DATA) {
	fake.signIn({ id: 'u1', name: 'Ana García', profile: null });
	fake.stats = stats;
	fake.statsState = 'ready';
}

const tile = (label: string) => screen.getByText(label).closest('div')!.parentElement!;
const isInert = (el: Element | null) =>
	el !== null &&
	(el.hasAttribute('inert') || (el as HTMLElement & { inert?: boolean }).inert === true);
const inertRegion = (container: HTMLElement) =>
	[...container.querySelectorAll('div')].find((el) => isInert(el)) ?? null;
const openDialog = async () => {
	await fireEvent.click(screen.getByRole('button', { name: 'Eliminar mi cuenta' }));
	return screen.getByRole('dialog');
};
const key = (target: Element, k: string, init: KeyboardEventInit = {}) =>
	fireEvent.keyDown(target, { key: k, ...init });

beforeEach(() => {
	i18n.set('es');
	toasts.items = [];
	fake.reset();
	vi.mocked(goto).mockClear();
});

describe('Stats screen', () => {
	it('sets the document title to "{brand} · Mis estadísticas" and asks the store for the session', () => {
		render(Stats);
		expect(document.title).toBe('Tutti Frutti · Mis estadísticas');
		expect(fake.initCalls).toBe(1);
	});

	describe('loading', () => {
		it('while the session loads: a busy skeleton, announced, and nothing else', () => {
			fake.status = 'loading';
			const { container } = render(Stats);
			expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
			expect(screen.getByRole('status').textContent).toBe('Cargando tus estadísticas…');
			expect(screen.queryByRole('button', { name: /Cerrar sesión|Eliminar/ })).toBeNull();
			expect(fake.loadStatsCalls).toBe(0);
		});

		it('signed in, numbers on the way: loads them once and shows a skeleton meanwhile', () => {
			fake.signIn();
			fake.statsState = 'loading';
			const { container } = render(Stats);
			expect(fake.loadStatsCalls).toBe(1);
			expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
			expect(screen.getByRole('status').textContent).toBe('Cargando tus estadísticas…');
		});
	});

	describe('data', () => {
		beforeEach(() => ready());

		it('five tiles with the numbers, the name and a way home', () => {
			render(Stats);
			expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Mis estadísticas');
			expect(screen.getByText('Ana García')).toBeTruthy();
			expect(within(tile('Partidas jugadas')).getByText('12')).toBeTruthy();
			expect(within(tile('Victorias')).getByText('5')).toBeTruthy();
			expect(within(tile('Puntos totales')).getByText('1240')).toBeTruthy();
			expect(within(tile('Mejor ronda')).getByText('80')).toBeTruthy();
			expect(within(tile('Respuestas únicas')).getByText('37')).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Volver al inicio' }).getAttribute('href')).toBe('/');
		});

		it('derived numbers: win rate and average points per game', () => {
			render(Stats);
			expect(within(tile('Victorias')).getByText('42 % de tus partidas')).toBeTruthy();
			expect(within(tile('Puntos totales')).getByText('103,3 por partida')).toBeTruthy();
		});

		it('announces the result to screen readers', () => {
			render(Stats);
			const live = screen.getByRole('status');
			expect(live.textContent).toBe('Estadísticas cargadas');
			expect(live.getAttribute('aria-live') ?? 'polite').toBe('polite');
		});

		it('is translated, with English number formatting', () => {
			i18n.set('en');
			render(Stats);
			expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('My stats');
			expect(within(tile('Total points')).getByText('1,240')).toBeTruthy();
			expect(within(tile('Wins')).getByText('42% of your games')).toBeTruthy();
			expect(within(tile('Total points')).getByText('103.3 per game')).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Back to home' })).toBeTruthy();
			expect(document.title).toBe('Tutti Frutti · My stats');
		});

		it('is translated, with Dutch number formatting (1.240 and 103,3)', () => {
			i18n.set('nl');
			render(Stats);
			expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Mijn statistieken');
			expect(within(tile('Totaal punten')).getByText('1.240')).toBeTruthy();
			expect(within(tile('Gewonnen')).getByText('42% van je spellen')).toBeTruthy();
			expect(within(tile('Totaal punten')).getByText('103,3 per spel')).toBeTruthy();
			expect(within(tile('Gespeelde spellen')).getByText('12')).toBeTruthy();
			expect(within(tile('Unieke antwoorden')).getByText('37')).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Terug naar het beginscherm' })).toBeTruthy();
			expect(document.title).toBe('Tutti Frutti · Mijn statistieken');
			expect(document.body.textContent).not.toMatch(/[¡¿ñ]/);
		});

		it('a perfect record and a single game still produce sane numbers', () => {
			fake.stats = { gamesPlayed: 1, wins: 1, totalPoints: 50, bestRound: 50, uniqueAnswers: 5 };
			render(Stats);
			expect(within(tile('Victorias')).getByText('100 % de tus partidas')).toBeTruthy();
			expect(within(tile('Puntos totales')).getByText('50 por partida')).toBeTruthy();
		});
	});

	describe('empty', () => {
		it('no games yet: a friendly message and a button to play, never NaN or a division by zero', () => {
			ready(NONE);
			const { container } = render(Stats);
			expect(
				screen.getByRole('heading', { name: 'Todavía no jugaste ninguna partida' })
			).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Jugar una partida' }).getAttribute('href')).toBe(
				'/'
			);
			expect(screen.queryByText('Partidas jugadas')).toBeNull();
			expect(container.textContent).not.toMatch(/NaN|Infinity|%/);
			expect(screen.getByRole('status').textContent).toBe('Todavía no jugaste ninguna partida');
			// the account actions stay available
			expect(screen.getByRole('button', { name: 'Cerrar sesión' })).toBeTruthy();
		});
	});

	describe('error', () => {
		it('says what failed, offers Retry (which reloads) and keeps the way home', async () => {
			fake.signIn();
			fake.statsState = 'error';
			fake.statsError = 'server_busy';
			render(Stats);
			expect(
				screen.getByRole('heading', { name: 'No pudimos cargar tus estadísticas' })
			).toBeTruthy();
			expect(screen.getByText('El servidor está ocupado. Probá de nuevo en un rato.')).toBeTruthy();
			expect(screen.getByRole('status').textContent).toBe('No pudimos cargar tus estadísticas');
			expect(screen.getByRole('link', { name: 'Volver al inicio' })).toBeTruthy();
			expect(fake.loadStatsCalls).toBe(1);
			await fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }));
			expect(fake.loadStatsCalls).toBe(2);
		});

		it('a session that could not even be loaded (offline) retries the session, not the stats', async () => {
			fake.initError = 'network';
			render(Stats);
			expect(
				screen.getByText('No hay conexión con el servidor. Revisá tu internet y probá de nuevo.')
			).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }));
			expect(fake.initCalls).toBe(2);
			expect(fake.loadStatsCalls).toBe(0);
		});
	});

	describe('not signed in', () => {
		it('a friendly prompt with the Google button (returns Home), never a blank page', async () => {
			render(Stats);
			expect(
				screen.getByRole('heading', { name: 'Iniciá sesión para ver tus estadísticas' })
			).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Volver al inicio' })).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Iniciar sesión con Google' }));
			expect(fake.loginCalls).toEqual(['/']);
			expect(fake.loadStatsCalls).toBe(0);
			expect(screen.queryByRole('button', { name: /Eliminar|Cerrar sesión/ })).toBeNull();
		});

		it('Google not configured: says sign-in is unavailable instead of a dead button', () => {
			fake.googleEnabled = false;
			render(Stats);
			expect(screen.getByText('El inicio de sesión no está disponible por ahora.')).toBeTruthy();
			expect(screen.queryByRole('button', { name: /Google/ })).toBeNull();
		});

		it('signing out elsewhere swaps the numbers for the prompt', async () => {
			ready();
			render(Stats);
			expect(screen.getByText('Mejor ronda')).toBeTruthy();
			fake.loggedIn = false;
			fake.user = null;
			fake.stats = null;
			await tick();
			expect(screen.queryByText('Mejor ronda')).toBeNull();
			expect(
				screen.getByRole('heading', { name: 'Iniciá sesión para ver tus estadísticas' })
			).toBeTruthy();
		});
	});

	describe('sign out', () => {
		beforeEach(() => ready());

		it('signs out, says so and goes Home', async () => {
			render(Stats);
			await fireEvent.click(screen.getByRole('button', { name: 'Cerrar sesión' }));
			expect(fake.logoutCalls).toBe(1);
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
			expect(toastTexts()).toEqual(['Cerraste sesión.']);
		});

		it('a failure stays on the screen with the reason', async () => {
			fake.onLogout = () => Promise.resolve({ ok: false, code: 'rate_limited' });
			render(Stats);
			await fireEvent.click(screen.getByRole('button', { name: 'Cerrar sesión' }));
			await waitFor(() =>
				expect(toastTexts()).toEqual(['Vas muy rápido. Esperá un momento y probá de nuevo.'])
			);
			expect(goto).not.toHaveBeenCalled();
			expect(screen.getByText('Mejor ronda')).toBeTruthy();
		});
	});

	describe('delete my account', () => {
		beforeEach(() => ready());

		it('asks first: a modal that explains profile and stats are erased, focus on Cancel; nothing is deleted yet', async () => {
			render(Stats);
			const dialog = await openDialog();
			expect(dialog.getAttribute('aria-modal')).toBe('true');
			expect(screen.getByRole('dialog', { name: '¿Eliminar tu cuenta?' })).toBe(dialog);
			expect(dialog.getAttribute('aria-describedby')).toBe('delete-text');
			const text = dialog.querySelector('#delete-text')!.textContent!;
			expect(text).toContain('perfil guardado');
			expect(text).toContain('estadísticas');
			expect(text).toContain('No se puede deshacer');
			expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancelar' }));
			expect(fake.deleteCalls).toBe(0);
		});

		it('keeps the page behind the dialog out of reach while it is open', async () => {
			const { container } = render(Stats);
			expect(inertRegion(container)).toBeNull();
			await openDialog();
			const behind = inertRegion(container)!;
			expect(behind.textContent).toContain('Mis estadísticas');
			expect(behind.contains(screen.getByRole('dialog'))).toBe(false);
			await key(screen.getByRole('dialog'), 'Escape');
			expect(inertRegion(container)).toBeNull();
		});

		it('Escape and Cancel close it without deleting, and focus goes back to the trigger', async () => {
			render(Stats);
			const trigger = screen.getByRole('button', { name: 'Eliminar mi cuenta' });
			await openDialog();
			await key(screen.getByRole('dialog'), 'Escape');
			expect(screen.queryByRole('dialog')).toBeNull();
			await waitFor(() => expect(document.activeElement).toBe(trigger));

			await openDialog();
			await fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }));
			expect(screen.queryByRole('dialog')).toBeNull();
			await waitFor(() => expect(document.activeElement).toBe(trigger));
			expect(fake.deleteCalls).toBe(0);
			expect(goto).not.toHaveBeenCalled();
		});

		it('traps Tab (and Shift+Tab) between its two buttons', async () => {
			render(Stats);
			await openDialog();
			const dialog = screen.getByRole('dialog');
			const confirm = within(dialog).getByRole('button', { name: 'Sí, eliminar mi cuenta' });
			const cancel = within(dialog).getByRole('button', { name: 'Cancelar' });
			expect(document.activeElement).toBe(cancel);
			await key(cancel, 'Tab');
			expect(document.activeElement).toBe(confirm);
			await key(confirm, 'Tab');
			expect(document.activeElement).toBe(cancel);
			await key(cancel, 'Tab', { shiftKey: true });
			expect(document.activeElement).toBe(confirm);
			await key(confirm, 'Tab', { shiftKey: true });
			expect(document.activeElement).toBe(cancel);
			// focus that somehow escaped is pulled back in
			(document.body as HTMLElement).focus();
			await key(document.body, 'Tab');
			expect(dialog.contains(document.activeElement)).toBe(true);
		});

		it('confirming deletes the account, says so and goes Home', async () => {
			render(Stats);
			await openDialog();
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, eliminar mi cuenta' }));
			expect(fake.deleteCalls).toBe(1);
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
			expect(toastTexts()).toEqual(['Tu cuenta fue eliminada.']);
			expect(screen.queryByRole('dialog')).toBeNull();
		});

		it('while deleting it shows progress, ignores second taps and Escape, and keeps focus inside', async () => {
			const pending = deferred<Outcome>();
			fake.onDelete = () => pending.promise;
			render(Stats);
			await openDialog();
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, eliminar mi cuenta' }));
			const busy = await screen.findByRole('button', { name: 'Eliminando…' });
			expect(busy.getAttribute('aria-busy')).toBe('true');
			await fireEvent.click(busy);
			await fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }));
			await key(screen.getByRole('dialog'), 'Escape');
			expect(fake.deleteCalls).toBe(1);
			expect(screen.getByRole('dialog')).toBeTruthy();
			pending.resolve({ ok: true });
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
		});

		it('a failure stays in the dialog with the reason, and a retry works', async () => {
			fake.onDelete = () => Promise.resolve({ ok: false, code: 'server_busy' });
			render(Stats);
			await openDialog();
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, eliminar mi cuenta' }));
			const alert = await screen.findByRole('alert');
			expect(alert.textContent).toBe('El servidor está ocupado. Probá de nuevo en un rato.');
			expect(screen.getByRole('dialog')).toBeTruthy();
			expect(goto).not.toHaveBeenCalled();

			fake.onDelete = () => Promise.resolve({ ok: true });
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, eliminar mi cuenta' }));
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
			expect(fake.deleteCalls).toBe(2);
		});

		it('an expired session closes the dialog and explains', async () => {
			fake.onDelete = async () => {
				fake.loggedIn = false; // what the store does on a 401
				return { ok: false, code: 'unauthorized' };
			};
			render(Stats);
			await openDialog();
			await fireEvent.click(screen.getByRole('button', { name: 'Sí, eliminar mi cuenta' }));
			await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
			expect(toastTexts()).toEqual(['Tu sesión venció. Iniciá sesión de nuevo.']);
			expect(goto).not.toHaveBeenCalled();
		});

		it('the dialog is translated', async () => {
			i18n.set('en');
			render(Stats);
			await fireEvent.click(screen.getByRole('button', { name: 'Delete my account' }));
			const dialog = screen.getByRole('dialog', { name: 'Delete your account?' });
			expect(dialog.textContent).toContain("can't be undone");
			expect(within(dialog).getByRole('button', { name: 'Yes, delete my account' })).toBeTruthy();
			expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeTruthy();
		});
	});
});
