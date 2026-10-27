// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto, replaceState } from '$app/navigation';
import { account } from '$lib/account';
import type { FakeAccount } from '$lib/account/fake.svelte';
import { fieldProblems, headingOnes } from '$lib/a11y.testkit';
import { ApiError, api, type RoomInfo } from '$lib/api';
import { hasRoomMark, clearRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { toasts } from '$lib/toast.svelte';
import Home from './+page.svelte';

const route = vi.hoisted(() => ({ url: new URL('http://localhost/'), state: {} }));

// SvelteKit runs `afterNavigate` callbacks when the page mounts (type "enter").
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

const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	...over
});

const fake = account as unknown as FakeAccount;

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
		fake.reset();
		route.url = new URL('http://localhost/');
		vi.mocked(goto).mockClear();
		vi.mocked(replaceState).mockClear();
		vi.mocked(api.createRoom).mockReset();
		vi.mocked(api.getRoom).mockReset();
	});

	describe('accessibility', () => {
		it('has exactly one h1 (the game name) in every language', () => {
			for (const locale of ['es', 'en', 'nl'] as const) {
				i18n.set(locale);
				const { unmount } = render(Home);
				// the wordmark is one span per word, so the text has no space between them
				expect(headingOnes(document.body), locale).toEqual(['TuttiFrutti']);
				unmount();
			}
		});

		it('gives every field a stable id, a name and an autocomplete value', () => {
			render(Home);
			const nickname = document.getElementById('nickname') as HTMLInputElement;
			// not "nickname": it is a valid token, but Chrome's DevTools flags it as "wrong but well intended"
			expect([nickname.name, nickname.getAttribute('autocomplete')]).toEqual(['nickname', 'off']);
			expect(nickname.getAttribute('autocapitalize')).toBe('words');
			const boxes = screen.getAllByRole<HTMLInputElement>('textbox').filter((b) => b !== nickname);
			expect(boxes.map((b) => [b.id, b.name, b.getAttribute('autocomplete')])).toEqual([
				['room-code-1', 'room-code-1', 'off'],
				['room-code-2', 'room-code-2', 'off'],
				['room-code-3', 'room-code-3', 'off'],
				['room-code-4', 'room-code-4', 'off']
			]);
			// the typing aids stay as they were
			for (const box of boxes) {
				expect(box.getAttribute('autocorrect')).toBe('off');
				expect(box.getAttribute('autocapitalize')).toBe('characters');
			}
			expect(fieldProblems(document.body)).toEqual([]);
		});
	});

	describe('support link', () => {
		it('sits next to the privacy link, outside the SPA, in every language', () => {
			for (const [locale, support, privacy] of [
				['es', /^Invitame un café/, 'Privacidad'],
				['en', /^Support the game/, 'Privacy policy'],
				['nl', /^Steun het spel/, 'Privacybeleid']
			] as const) {
				i18n.set(locale);
				const { unmount } = render(Home);
				const link = screen.getByRole('link', { name: support });
				expect(link.getAttribute('target'), locale).toBe('_blank');
				expect(link.getAttribute('href'), locale).toMatch(
					/^https:\/\/(cafecito\.app|ko-fi\.com)\//
				);
				expect(link.parentElement, locale).toBe(
					screen.getByRole('link', { name: privacy }).parentElement
				);
				unmount();
			}
		});
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

	describe('account', () => {
		const googleButton = () =>
			screen.queryByRole('button', { name: /Iniciar sesión con Google|Sign in with Google/ });
		const statsLink = () => screen.queryByRole('link', { name: /Mis estadísticas|My stats/ });
		const signOut = () => screen.queryByRole('button', { name: /Cerrar sesión|Sign out/ });

		it('asks the account store for the session once, when it opens', () => {
			render(Home);
			expect(fake.initCalls).toBe(1);
		});

		describe.each([
			['es', 'Iniciar sesión con Google', 'Mis estadísticas', 'Cerrar sesión', 'Cerraste sesión.'],
			['en', 'Sign in with Google', 'My stats', 'Sign out', 'You signed out.']
		] as const)('in %s', (lang, signIn, stats, out, signedOut) => {
			beforeEach(() => i18n.set(lang));

			it('guest with Google enabled: the sign-in action starts a real login that returns Home', async () => {
				render(Home);
				await fireEvent.click(screen.getByRole('button', { name: signIn }));
				expect(fake.loginCalls).toEqual(['/']);
				expect(statsLink()).toBeNull();
				expect(signOut()).toBeNull();
			});

			it('guest with Google disabled: no account controls at all (no dead link)', () => {
				fake.googleEnabled = false;
				render(Home);
				expect(googleButton()).toBeNull();
				expect(statsLink()).toBeNull();
				expect(signOut()).toBeNull();
				expect(screen.queryByText(/Google/)).toBeNull();
			});

			it('signed in: who you are, My stats (-> /stats) and Sign out; no sign-in action', async () => {
				fake.signIn({ id: 'u1', name: 'Ana García', profile: null });
				render(Home);
				expect(screen.getByText('Ana García')).toBeTruthy();
				expect(screen.getByRole('link', { name: stats }).getAttribute('href')).toBe('/stats');
				expect(googleButton()).toBeNull();

				await fireEvent.click(screen.getByRole('button', { name: out }));
				expect(fake.logoutCalls).toBe(1);
				await waitFor(() => expect(toastTexts()).toEqual([signedOut]));
				// back to a guest: the sign-in action returns
				expect(screen.getByRole('button', { name: signIn })).toBeTruthy();
				expect(statsLink()).toBeNull();
			});
		});

		it('a failed sign-out stays signed in and says why', async () => {
			fake.signIn();
			fake.onLogout = () => Promise.resolve({ ok: false, code: 'network' });
			render(Home);
			await fireEvent.click(screen.getByRole('button', { name: 'Cerrar sesión' }));
			await waitFor(() =>
				expect(toastTexts()).toEqual([
					'No hay conexión con el servidor. Revisá tu internet y probá de nuevo.'
				])
			);
			expect(screen.getByRole('link', { name: 'Mis estadísticas' })).toBeTruthy();
		});

		it('shows progress while signing out and ignores a second tap', async () => {
			fake.signIn();
			const pending = deferred<{ ok: true }>();
			fake.onLogout = () => pending.promise;
			render(Home);
			await fireEvent.click(screen.getByRole('button', { name: 'Cerrar sesión' }));
			const busy = await screen.findByRole('button', { name: 'Cerrando sesión…' });
			expect(busy.getAttribute('aria-busy')).toBe('true');
			await fireEvent.click(busy);
			expect(fake.logoutCalls).toBe(1);
			pending.resolve({ ok: true });
			await waitFor(() => expect(toastTexts()).toEqual(['Cerraste sesión.']));
		});

		it('while the session loads nothing flickers: no controls, space reserved, then the right variant', async () => {
			fake.status = 'loading';
			const { container } = render(Home);
			expect(googleButton()).toBeNull();
			expect(statsLink()).toBeNull();
			expect(signOut()).toBeNull();
			expect(container.querySelector('.h-12[aria-hidden="true"]')).not.toBeNull();
			fake.status = 'ready';
			await tick();
			expect(googleButton()).not.toBeNull();
			expect(container.querySelector('.h-12[aria-hidden="true"]')).toBeNull();
		});

		it('follows the saved profile adopted after load without fighting the typing', async () => {
			render(Home);
			const input = screen.getByLabelText(/Tu apodo/) as HTMLInputElement;
			expect(input.value).toBe('Ana');
			profile.setNickname('Anita');
			profile.setAvatar({ emoji: '🍇', color: 'grape' });
			await tick();
			expect(input.value).toBe('Anita');
			expect(screen.getByRole('button', { name: '🍇' }).getAttribute('aria-pressed')).toBe('true');

			await fireEvent.input(input, { target: { value: 'Anitaa ' } }); // trailing space while typing
			await tick();
			expect(input.value).toBe('Anitaa ');
			expect(profile.nickname).toBe('Anitaa');
		});
	});

	describe('?login= result from the OAuth callback', () => {
		it.each([
			['failed', 'es', 'No pudimos iniciar sesión. Probá de nuevo.'],
			['cancelled', 'es', 'Cancelaste el inicio de sesión.'],
			['failed', 'en', "We couldn't sign you in. Try again."],
			['cancelled', 'en', 'Sign-in cancelled.']
		] as const)(
			'%s (%s): a translated toast, then the parameter is removed',
			(reason, lang, text) => {
				i18n.set(lang);
				route.url = new URL(`http://localhost/?login=${reason}`);
				render(Home);
				expect(toastTexts()).toEqual([text]);
				expect(replaceState).toHaveBeenCalledTimes(1);
				expect(replaceState).toHaveBeenCalledWith('/', route.state);
			}
		);

		it('keeps every other query parameter and the hash', () => {
			route.url = new URL('http://localhost/?x=1&login=failed&y=2#top');
			render(Home);
			expect(replaceState).toHaveBeenCalledWith('/?x=1&y=2#top', route.state);
		});

		it.each(['', '?login=', '?login=pwned', '?login=FAILED', '?other=1'])(
			'%j: nothing to say, nothing to clean',
			(query) => {
				route.url = new URL(`http://localhost/${query}`);
				render(Home);
				expect(toastTexts()).toEqual([]);
				expect(replaceState).not.toHaveBeenCalled();
			}
		);
	});
});
