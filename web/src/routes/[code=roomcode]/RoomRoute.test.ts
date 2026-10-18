// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView } from '$lib/game/fixtures';
import { clearRoomMark, hasRoomMark, markRoom } from '$lib/game/roomMark';
import type { ClosedReason } from '$lib/game/session';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { toasts } from '$lib/toast.svelte';
import RoomRoute from './RoomRoute.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));
vi.mock('$lib/api', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api')>()),
	api: {
		getRoom: vi.fn(() =>
			Promise.resolve({
				code: 'KT7P',
				phase: 'lobby',
				players: 2,
				maxPlayers: 10,
				lang: 'es',
				joinable: true
			})
		)
	}
}));
vi.mock('$lib/screens/RoomScreen.svelte', async () => ({
	default: (await import('./RoomScreenStub.svelte')).default
}));

function factory(initial: () => FakeSession = () => new FakeSession(makeView())) {
	const created: FakeSession[] = [];
	const createSession = vi.fn((code: string) => {
		const s = initial();
		s.code = code;
		created.push(s);
		return s;
	});
	return { createSession, created };
}

describe('room route', () => {
	beforeEach(() => {
		i18n.set('es');
		profile.setNickname('Ana');
		clearRoomMark('KT7P');
		toasts.items = [];
		vi.mocked(goto).mockClear();
	});

	describe('entering', () => {
		it('without the tab flag it shows the gate and does not connect', async () => {
			const { createSession } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			expect(await screen.findByRole('button', { name: 'Entrar a la sala' })).toBeTruthy();
			expect(createSession).not.toHaveBeenCalled();
		});

		it('pressing Join marks the tab, connects and leaves the gate', async () => {
			const { createSession } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			const join = await screen.findByRole('button', { name: 'Entrar a la sala' });
			await waitFor(() => expect((join as HTMLButtonElement).disabled).toBe(false));
			await fireEvent.click(join);
			expect(hasRoomMark('KT7P')).toBe(true);
			expect(createSession).toHaveBeenCalledWith('KT7P');
			expect(await screen.findByTestId('room-screen')).toBeTruthy();
			expect(screen.queryByRole('button', { name: 'Entrar a la sala' })).toBeNull();
		});

		it('with the tab flag and a nickname it connects straight away (no gate)', () => {
			markRoom('KT7P');
			const { createSession } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			expect(createSession).toHaveBeenCalledOnce();
			expect(screen.getByTestId('room-screen').textContent).toBe('lobby:3');
			expect(screen.queryByRole('button', { name: 'Entrar a la sala' })).toBeNull();
		});

		it('with the flag but no nickname it still shows the gate', async () => {
			markRoom('KT7P');
			profile.setNickname('');
			const { createSession } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			expect(await screen.findByLabelText('Tu apodo')).toBeTruthy();
			expect(createSession).not.toHaveBeenCalled();
		});

		it('a flag of another room does not count', async () => {
			markRoom('ABCD');
			const { createSession } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			await screen.findByLabelText('Tu apodo');
			expect(createSession).not.toHaveBeenCalled();
			clearRoomMark('ABCD');
		});
	});

	describe('connection states', () => {
		beforeEach(() => markRoom('KT7P'));

		it('connecting: nothing for 300 ms, then a friendly screen', async () => {
			vi.useFakeTimers();
			try {
				const { createSession } = factory(() => {
					const s = new FakeSession(null);
					s.status = 'connecting';
					return s;
				});
				render(RoomRoute, { code: 'KT7P', createSession });
				// only the screen-reader-only heading at first: nothing to see
				expect(screen.queryByText('Entrando a la sala KT7P')).toBeNull();
				expect(screen.getByText('Conectando…').classList.contains('sr-only')).toBe(true);
				await vi.advanceTimersByTimeAsync(300);
				expect(screen.getByText('Conectando…').classList.contains('sr-only')).toBe(false);
				expect(screen.getByText('Entrando a la sala KT7P')).toBeTruthy();
				expect(screen.queryByTestId('room-screen')).toBeNull();
			} finally {
				vi.useRealTimers();
			}
		});

		it('switches to the phase screen when the first state arrives', async () => {
			const { createSession, created } = factory(() => {
				const s = new FakeSession(null);
				s.status = 'connecting';
				return s;
			});
			render(RoomRoute, { code: 'KT7P', createSession });
			expect(screen.queryByTestId('room-screen')).toBeNull();
			created[0].view = makeView({ phase: 'letter' });
			created[0].status = 'connected';
			expect(await screen.findByText('letter:3')).toBeTruthy();
		});

		it('reconnecting keeps the last screen and shows the banner', async () => {
			const { createSession, created } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			expect(screen.queryByText('Reconectando…')).toBeNull();
			created[0].status = 'reconnecting';
			await tick();
			expect(screen.getByTestId('room-screen')).toBeTruthy();
			expect(screen.getByText('Reconectando…')).toBeTruthy();
			created[0].status = 'connected';
			await tick();
			expect(screen.queryByText('Reconectando…')).toBeNull();
		});

		it('destroys the session when the route goes away', () => {
			const { createSession, created } = factory();
			const { unmount } = render(RoomRoute, { code: 'KT7P', createSession });
			unmount();
			expect(created[0].sent.filter((c) => c.cmd === 'destroy')).toHaveLength(1);
		});
	});

	describe('closed sessions', () => {
		beforeEach(() => markRoom('KT7P'));

		async function closeWith(reason: ClosedReason, joinError: string | null = null) {
			const f = factory();
			render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
			const s = f.created[0];
			s.closedReason = reason;
			s.joinError = joinError;
			s.status = 'closed';
			await tick();
			return { ...f, s };
		}

		it.each<[ClosedReason, string]>([
			['room_gone', 'La sala ya no existe'],
			['kicked', 'Te sacaron de la sala'],
			['replaced', 'Esta sala se abrió en otro lugar'],
			['unsupported_version', 'Nueva versión disponible']
		])('%s shows its screen with a Home button', async (reason, title) => {
			await closeWith(reason);
			expect(screen.getByRole('heading', { name: title })).toBeTruthy();
			expect(screen.getByRole('link', { name: 'Ir al inicio' })).toBeTruthy();
			expect(screen.queryByTestId('room-screen')).toBeNull();
		});

		it.each<ClosedReason>(['room_gone', 'kicked', 'join_rejected'])(
			'%s clears the tab flag',
			async (reason) => {
				await closeWith(reason, 'lobby_full');
				expect(hasRoomMark('KT7P')).toBe(false);
			}
		);

		it.each<ClosedReason>(['replaced', 'unsupported_version'])(
			'%s keeps the tab flag (a reload comes back in)',
			async (reason) => {
				await closeWith(reason);
				expect(hasRoomMark('KT7P')).toBe(true);
			}
		);

		it('left goes Home directly and clears the flag', async () => {
			await closeWith('left');
			expect(goto).toHaveBeenCalledWith('/');
			expect(hasRoomMark('KT7P')).toBe(false);
			expect(screen.queryByRole('heading')).toBeNull();
		});

		it('replaced: "take my seat back" starts a brand new session', async () => {
			const { createSession, created } = await closeWith('replaced');
			await fireEvent.click(screen.getByRole('button', { name: 'Recuperar mi lugar acá' }));
			expect(createSession).toHaveBeenCalledTimes(2);
			expect(created[0].sent.some((c) => c.cmd === 'destroy')).toBe(true);
			expect(await screen.findByTestId('room-screen')).toBeTruthy();
			expect(
				screen.queryByRole('heading', { name: 'Esta sala se abrió en otro lugar' })
			).toBeNull();
		});

		it('join_rejected explains why and "try again" goes back to the gate', async () => {
			const { createSession } = await closeWith('join_rejected', 'lobby_full');
			expect(screen.getByText('La sala está llena.')).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Probar de nuevo' }));
			expect(await screen.findByRole('button', { name: 'Entrar a la sala' })).toBeTruthy();
			expect(createSession).toHaveBeenCalledOnce();
		});
	});

	describe('error toasts', () => {
		beforeEach(() => markRoom('KT7P'));

		it('shows a translated toast for every NEW error, once', async () => {
			const { createSession, created } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			created[0].fail('not_host', 'kick');
			await tick();
			expect(toasts.items.map((t) => t.text)).toEqual(['Solo el anfitrión puede hacer eso.']);
			await tick();
			expect(toasts.items).toHaveLength(1);
			created[0].fail('wrong_phase');
			await tick();
			expect(toasts.items.map((t) => t.text)).toEqual([
				'Solo el anfitrión puede hacer eso.',
				'Eso no se puede hacer en este momento.'
			]);
		});

		it('covers not_connected, unknown codes and English', async () => {
			i18n.set('en');
			const { createSession, created } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			created[0].fail('not_connected');
			await tick();
			created[0].fail('something_new');
			await tick();
			expect(toasts.items.map((t) => t.text)).toEqual([
				'Not connected to the room. Reconnecting…',
				'Something went wrong. Try again.'
			]);
			i18n.set('es');
		});

		it('does not toast the error that ended the session (the closed screen says it)', async () => {
			const { createSession, created } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			created[0].closedReason = 'room_gone';
			created[0].status = 'closed';
			created[0].fail('room_not_found');
			await tick();
			expect(toasts.items).toHaveLength(0);
		});

		it('an error that was already there when the session was created is shown too', async () => {
			const { createSession } = factory(() => {
				const s = new FakeSession(makeView());
				s.fail('not_host');
				return s;
			});
			render(RoomRoute, { code: 'KT7P', createSession });
			await tick();
			expect(toasts.items).toHaveLength(1);
		});
	});

	afterEach(() => clearRoomMark('KT7P'));
});
