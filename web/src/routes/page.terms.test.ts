// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { account } from '$lib/account';
import type { FakeAccount } from '$lib/account/fake.svelte';
import { ApiError, api, type RoomInfo } from '$lib/api';
import { clearRoomMark, hasRoomMark } from '$lib/game/roomMark';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { safetyApi } from '$lib/safety/api';
import { TERMS_DUE, TERMS_OK } from '$lib/safety/safety.testkit';
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

const info: RoomInfo = {
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	visibility: 'private',
	roomInstanceId: '01020304-0506-4708-890a-0b0c0d0e0f10'
};
const fake = account as unknown as FakeAccount;
const create = () => screen.getByRole('button', { name: /Creá una partida|Creando/ });
const join = () => screen.getByRole('button', { name: /Unite a una partida|Entrando/ });

beforeEach(() => {
	i18n.set('es');
	profile.setNickname('Ana');
	toasts.items = [];
	clearRoomMark('KT7P');
	fake.reset();
	terms.apply(TERMS_OK);
	vi.mocked(goto).mockClear();
	vi.mocked(api.createRoom).mockReset().mockResolvedValue({ code: 'KT7P' });
	vi.mocked(api.getRoom).mockReset().mockResolvedValue(info);
	vi.restoreAllMocks();
});
afterEach(() => cleanup());

describe('creating a game', () => {
	it('with the terms accepted it creates at once', async () => {
		render(Home);
		await fireEvent.click(create());
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(terms.open).toBe(false);
	});

	it('with the terms due it opens the gate first, creates nothing, and continues after the acceptance', async () => {
		terms.apply(TERMS_DUE);
		const accept = vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		render(Home);
		await fireEvent.click(create());
		await waitFor(() => expect(terms.open).toBe(true));
		expect(api.createRoom).not.toHaveBeenCalled();
		// the interrupted intent is kept: same nickname, same avatar, same visibility
		await terms.accept();
		expect(accept).toHaveBeenCalledWith({ version: '2026-10', locale: 'es', age13Confirmed: true });
		await waitFor(() => expect(api.createRoom).toHaveBeenCalledTimes(1));
		expect(api.createRoom).toHaveBeenCalledWith(
			expect.objectContaining({ name: 'Ana', lang: 'es', visibility: 'private' })
		);
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(hasRoomMark('KT7P')).toBe(true);
	});

	it('closing the gate creates nothing and frees the button', async () => {
		terms.apply(TERMS_DUE);
		render(Home);
		await fireEvent.click(create());
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		await waitFor(() => expect(create().getAttribute('aria-busy')).not.toBe('true'));
		expect(api.createRoom).not.toHaveBeenCalled();
		expect(goto).not.toHaveBeenCalled();
		expect(toasts.items).toHaveLength(0);
	});

	it('403 terms_required from a client that thought it had accepted: gate, then one more try', async () => {
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		vi.mocked(api.createRoom)
			.mockRejectedValueOnce(new ApiError(403, 'terms_required'))
			.mockResolvedValueOnce({ code: 'KT7P' });
		render(Home);
		await fireEvent.click(create());
		await waitFor(() => expect(terms.open).toBe(true));
		await terms.accept();
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(api.createRoom).toHaveBeenCalledTimes(2);
	});

	it('a second terms_required is shown as a message, not retried again', async () => {
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		vi.mocked(api.createRoom).mockRejectedValue(new ApiError(403, 'terms_required'));
		render(Home);
		await fireEvent.click(create());
		await waitFor(() => expect(terms.open).toBe(true));
		await terms.accept();
		await waitFor(() =>
			expect(toasts.items.map((t) => t.text)).toEqual([
				'Tenés que aceptar los términos para jugar.'
			])
		);
		expect(api.createRoom).toHaveBeenCalledTimes(2);
		expect(goto).not.toHaveBeenCalled();
	});

	it('join_unavailable (a block or a restriction) is the generic sentence', async () => {
		i18n.set('en');
		vi.mocked(api.createRoom).mockRejectedValue(new ApiError(403, 'join_unavailable'));
		render(Home);
		await fireEvent.click(screen.getByRole('button', { name: 'Create game' }));
		await waitFor(() =>
			expect(toasts.items.map((t) => t.text)).toEqual(["You can't join this game right now."])
		);
	});
});

describe('joining by code', () => {
	async function typeCode() {
		await fireEvent.input(screen.getByLabelText('Carácter 1 de 4'), { target: { value: 'KT7P' } });
	}

	it('checks the room, asks for the terms, and only then goes in', async () => {
		terms.apply(TERMS_DUE);
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		render(Home);
		await typeCode();
		await fireEvent.click(join());
		await waitFor(() => expect(terms.open).toBe(true));
		expect(api.getRoom).toHaveBeenCalledWith('KT7P');
		expect(goto).not.toHaveBeenCalled();
		await terms.accept();
		await waitFor(() => expect(goto).toHaveBeenCalledWith('/KT7P'));
		expect(hasRoomMark('KT7P')).toBe(true);
	});

	it('does not even ask for the terms when the room cannot be joined', async () => {
		terms.apply(TERMS_DUE);
		vi.mocked(api.getRoom).mockResolvedValue({ ...info, joinable: false, players: 10 });
		render(Home);
		await typeCode();
		await fireEvent.click(join());
		await waitFor(() => expect(toasts.items).toHaveLength(1));
		expect(terms.open).toBe(false);
	});

	it('declining the gate joins nothing', async () => {
		terms.apply(TERMS_DUE);
		render(Home);
		await typeCode();
		await fireEvent.click(join());
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		await waitFor(() => expect(join().getAttribute('aria-busy')).not.toBe('true'));
		expect(goto).not.toHaveBeenCalled();
		expect(hasRoomMark('KT7P')).toBe(false);
	});
});

describe('legal and safety entries', () => {
	it('keeps the privacy link in the footer row and adds a named button for the rest', () => {
		i18n.set('en');
		render(Home);
		expect(screen.getByRole('link', { name: 'Privacy policy' }).getAttribute('href')).toBe(
			'/privacy#en'
		);
		expect(screen.getByRole('button', { name: 'Terms, safety and legal' }).className).toContain(
			'size-12'
		);
	});

	it('the sheet links the terms, the privacy policy and the deletion page (real page loads) and the block list', async () => {
		i18n.set('nl');
		render(Home);
		await fireEvent.click(
			screen.getByRole('button', { name: 'Voorwaarden, veiligheid en juridisch' })
		);
		const dialog = await screen.findByRole('dialog', {
			name: 'Voorwaarden, veiligheid en juridisch'
		});
		const links = within(dialog).getAllByRole('link');
		expect(links.map((a) => [a.getAttribute('href'), a.getAttribute('rel')])).toEqual([
			['/terms#nl', 'external'],
			['/privacy#nl', 'external'],
			['/delete-account#nl', 'external']
		]);
		for (const link of links) expect(link.getAttribute('target')).toBeNull();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Geblokkeerde spelers' }));
		expect(await screen.findByRole('dialog', { name: 'Geblokkeerde spelers' })).toBeTruthy();
	});
});
