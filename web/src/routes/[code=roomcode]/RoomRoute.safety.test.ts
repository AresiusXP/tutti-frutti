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
import { safetyApi } from '$lib/safety/api';
import { blocks } from '$lib/safety/blocks.svelte';
import { TERMS_DUE, TERMS_OK } from '$lib/safety/safety.testkit';
import { terms } from '$lib/safety/terms.svelte';
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

function factory() {
	const created: FakeSession[] = [];
	const createSession = vi.fn((code: string) => {
		const s = new FakeSession(makeView());
		s.code = code;
		// a real session is connecting until its join is accepted
		s.status = 'connecting';
		created.push(s);
		return s;
	});
	return { createSession, created };
}

function refuse(s: FakeSession, joinError: string, reason: ClosedReason = 'join_rejected') {
	s.joinError = joinError;
	s.closedReason = reason;
	s.status = 'closed';
}

beforeEach(() => {
	i18n.set('es');
	profile.setNickname('Ana');
	clearRoomMark('KT7P');
	toasts.items = [];
	terms.apply(TERMS_OK);
	terms.dismiss();
	blocks.expectRemoval(null);
	vi.mocked(goto).mockClear();
	vi.restoreAllMocks();
});
afterEach(() => clearRoomMark('KT7P'));

describe('the terms before joining from the gate', () => {
	it('with the terms accepted, Join connects in the same tick', async () => {
		const { createSession } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		await fireEvent.click(await screen.findByRole('button', { name: 'Entrar a la sala' }));
		expect(createSession).toHaveBeenCalledTimes(1);
		expect(terms.open).toBe(false);
	});

	it('with the terms due, Join opens the gate and connects only after the acceptance', async () => {
		terms.apply(TERMS_DUE);
		const accept = vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		const { createSession } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		await fireEvent.click(await screen.findByRole('button', { name: 'Entrar a la sala' }));
		await waitFor(() => expect(terms.open).toBe(true));
		expect(createSession).not.toHaveBeenCalled();
		expect(hasRoomMark('KT7P')).toBe(false);
		await terms.accept();
		expect(accept).toHaveBeenCalledTimes(1);
		expect(await screen.findByTestId('room-screen')).toBeTruthy();
		expect(createSession).toHaveBeenCalledTimes(1);
		expect(hasRoomMark('KT7P')).toBe(true);
	});

	it('closing the gate connects to nothing and the player stays on the invite gate', async () => {
		terms.apply(TERMS_DUE);
		const { createSession } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		await fireEvent.click(await screen.findByRole('button', { name: 'Entrar a la sala' }));
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		await tick();
		expect(createSession).not.toHaveBeenCalled();
		expect(screen.getByRole('button', { name: 'Entrar a la sala' })).toBeTruthy();
	});
});

describe('terms_required answered to the join (a new version, an expired acceptance)', () => {
	beforeEach(() => markRoom('KT7P'));

	it('shows the terms (not an error screen), then joins once more', async () => {
		const accept = vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		refuse(created[0], 'terms_required');
		await tick();
		// no flash of the "no pudiste entrar" screen behind the gate
		expect(screen.queryByRole('heading', { name: 'No pudiste entrar' })).toBeNull();
		await waitFor(() => expect(terms.open).toBe(true));
		expect(terms.acceptanceRequired).toBe(true);
		expect(createSession).toHaveBeenCalledTimes(1);
		await terms.accept();
		expect(accept).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(createSession).toHaveBeenCalledTimes(2));
		expect(created[0].sent.some((c) => c.cmd === 'destroy')).toBe(true);
		expect(await screen.findByTestId('room-screen')).toBeTruthy();
	});

	it('declining goes back to the invite gate with the tab flag cleared', async () => {
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		refuse(created[0], 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		expect(await screen.findByRole('button', { name: 'Entrar a la sala' })).toBeTruthy();
		expect(hasRoomMark('KT7P')).toBe(false);
		expect(createSession).toHaveBeenCalledTimes(1);
	});

	it('a second refusal right after the acceptance is explained and never retried (no loop)', async () => {
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		refuse(created[0], 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
		await terms.accept();
		await waitFor(() => expect(createSession).toHaveBeenCalledTimes(2));
		refuse(created[1], 'terms_required');
		expect(await screen.findByText('Tenés que aceptar los términos para jugar.')).toBeTruthy();
		await tick();
		expect(createSession).toHaveBeenCalledTimes(2);
		expect(terms.open).toBe(false);
	});

	it('a join that held resets the allowance: a later new version asks again', async () => {
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		refuse(created[0], 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
		await terms.accept();
		await waitFor(() => expect(createSession).toHaveBeenCalledTimes(2));
		created[1].status = 'connected';
		await tick();
		refuse(created[1], 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
	});
});

describe('block and leave', () => {
	beforeEach(() => markRoom('KT7P'));

	it.each<ClosedReason>(['left', 'kicked', 'room_gone'])(
		'a socket closed as %s while a block is expected goes Home without a closed screen',
		async (reason) => {
			const { createSession, created } = factory();
			render(RoomRoute, { code: 'KT7P', createSession });
			blocks.expectRemoval('KT7P');
			created[0].closedReason = reason;
			created[0].status = 'closed';
			await tick();
			expect(goto).toHaveBeenCalledWith('/');
			expect(hasRoomMark('KT7P')).toBe(false);
			expect(screen.queryByRole('heading')).toBeNull();
		}
	);

	it('without a block in flight, kicked still shows its screen', async () => {
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		created[0].closedReason = 'kicked';
		created[0].status = 'closed';
		await tick();
		expect(screen.getByRole('heading', { name: 'Te sacaron de la sala' })).toBeTruthy();
		expect(goto).not.toHaveBeenCalled();
	});

	it("another room's expectation does not hide this one's closed screen", async () => {
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		blocks.expectRemoval('ZZZZ');
		created[0].closedReason = 'room_gone';
		created[0].status = 'closed';
		await tick();
		expect(screen.getByRole('heading', { name: 'La sala ya no existe' })).toBeTruthy();
	});

	it('leaving the route forgets the expectation', () => {
		const { createSession } = factory();
		const { unmount } = render(RoomRoute, { code: 'KT7P', createSession });
		blocks.expectRemoval('KT7P');
		unmount();
		expect(blocks.expecting).toBeNull();
	});
});

describe('a refused join for a safety reason', () => {
	beforeEach(() => markRoom('KT7P'));

	it('says only that the player cannot join right now', async () => {
		i18n.set('en');
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		refuse(created[0], 'join_unavailable');
		await tick();
		expect(screen.getByText("You can't join this game right now.")).toBeTruthy();
		expect(screen.queryByText(/block|restrict|ban/i)).toBeNull();
	});
});
