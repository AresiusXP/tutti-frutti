// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView } from '$lib/game/fixtures';
import { clearRoomMark, markRoom } from '$lib/game/roomMark';
import type { ClosedReason } from '$lib/game/session';
import { i18n } from '$lib/i18n.svelte';
import { profile } from '$lib/profile.svelte';
import { RECENT_ROOM_KEY, loadRecentRoom, rememberRecentRoom } from '$lib/resume/recent';
import { safetyApi } from '$lib/safety/api';
import { blocks } from '$lib/safety/blocks.svelte';
import { TERMS_OK } from '$lib/safety/safety.testkit';
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

const ID = '01020304-0506-4708-890a-0b0c0d0e0f10';
const NEWER_ID = '11121314-1516-4718-991a-1b1c1d1e1f20';

/** Sessions that start connecting, like a real one, and get their first state when the test says so. */
function factory() {
	const created: FakeSession[] = [];
	const createSession = vi.fn((code: string) => {
		const s = new FakeSession(makeView());
		s.code = code;
		s.status = 'connecting';
		s.roomInstanceId = null;
		created.push(s);
		return s;
	});
	return { createSession, created };
}

function joined(s: FakeSession, roomInstanceId: string | null = ID) {
	s.roomInstanceId = roomInstanceId;
	s.status = 'connected';
}

function close(s: FakeSession, reason: ClosedReason, joinError: string | null = null) {
	s.closedReason = reason;
	s.joinError = joinError;
	s.status = 'closed';
}

const slot = () => localStorage.getItem(RECENT_ROOM_KEY);

beforeEach(() => {
	i18n.set('es');
	profile.setNickname('Ana');
	clearRoomMark('KT7P');
	localStorage.clear();
	toasts.items = [];
	terms.apply(TERMS_OK);
	terms.dismiss();
	blocks.expectRemoval(null);
	vi.mocked(goto).mockClear();
	vi.restoreAllMocks();
});
afterEach(() => {
	clearRoomMark('KT7P');
	localStorage.clear();
});

describe('the room remembered for "Resume recent game"', () => {
	beforeEach(() => markRoom('KT7P'));

	it('is written when the server confirms the seat (the first state) and not before', async () => {
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		await tick();
		expect(slot()).toBeNull();
		const before = Date.now();
		joined(created[0]);
		await tick();
		const saved = JSON.parse(slot()!) as Record<string, unknown>;
		expect(Object.keys(saved).sort()).toEqual(['code', 'lastJoinedAt', 'roomInstanceId']);
		expect(saved.code).toBe('KT7P');
		expect(saved.roomInstanceId).toBe(ID);
		expect(saved.lastJoinedAt as number).toBeGreaterThanOrEqual(before);
		expect(slot()).not.toContain('Ana'); // no nickname, no avatar, no answers
	});

	it('is also written after pressing Join on the invite gate', async () => {
		clearRoomMark('KT7P');
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		await fireEvent.click(await screen.findByRole('button', { name: 'Entrar a la sala' }));
		expect(slot()).toBeNull();
		joined(created[0]);
		await waitFor(() => expect(loadRecentRoom()?.code).toBe('KT7P'));
	});

	it('writes nothing when the state carries no valid instance id', async () => {
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		joined(created[0], null);
		await tick();
		joined(created[0], 'not-a-uuid');
		await tick();
		expect(slot()).toBeNull();
	});

	it('refreshes the time on a reconnect and keeps a single slot, replacing another room', async () => {
		rememberRecentRoom('BCDF', NEWER_ID, Date.now() - 1000);
		const { createSession, created } = factory();
		render(RoomRoute, { code: 'KT7P', createSession });
		joined(created[0]);
		await tick();
		expect(loadRecentRoom()).toMatchObject({ code: 'KT7P', roomInstanceId: ID });
		expect(localStorage.length).toBe(1);
		created[0].status = 'reconnecting';
		await tick();
		expect(loadRecentRoom()?.code).toBe('KT7P'); // a lost connection forgets nothing
		created[0].status = 'connected';
		await tick();
		expect(loadRecentRoom()?.code).toBe('KT7P');
	});

	it('survives leaving the screen (Back to Home): the seat is still ours', async () => {
		const { createSession, created } = factory();
		const { unmount } = render(RoomRoute, { code: 'KT7P', createSession });
		joined(created[0]);
		await tick();
		unmount();
		expect(loadRecentRoom()).toMatchObject({ code: 'KT7P', roomInstanceId: ID });
	});
});

describe('forgetting it', () => {
	beforeEach(() => markRoom('KT7P'));

	async function inRoomThenClose(reason: ClosedReason, joinError: string | null = null) {
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		joined(f.created[0]);
		await tick();
		expect(loadRecentRoom()?.code).toBe('KT7P');
		close(f.created[0], reason, joinError);
		await tick();
		return f;
	}

	it.each<[string, ClosedReason, string | null]>([
		['an explicit leave (also "Block and leave": it closes the session locally)', 'left', null],
		['being kicked', 'kicked', null],
		['a closed room', 'room_gone', null],
		['a refused join (full or already started)', 'join_rejected', 'lobby_full'],
		['a refusal for safety reasons (join_unavailable)', 'join_rejected', 'join_unavailable']
	])('%s clears it', async (_name, reason, joinError) => {
		await inRoomThenClose(reason, joinError);
		expect(slot()).toBeNull();
	});

	it('a refused join that never got a state clears the slot it was resumed from', async () => {
		rememberRecentRoom('KT7P', ID);
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		close(f.created[0], 'room_gone');
		await tick();
		expect(slot()).toBeNull();
	});

	it('a refused join that never got a state leaves alone a newer room another tab remembered for the same code', async () => {
		rememberRecentRoom('KT7P', ID);
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		rememberRecentRoom('KT7P', NEWER_ID); // another tab joined a new room that reuses the code
		close(f.created[0], 'room_gone');
		await tick();
		expect(loadRecentRoom()).toMatchObject({ code: 'KT7P', roomInstanceId: NEWER_ID });
	});

	it('a tab that never held the slot has no claim on it when its join fails', async () => {
		rememberRecentRoom('BCDF', NEWER_ID);
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		close(f.created[0], 'join_rejected', 'lobby_full');
		await tick();
		expect(loadRecentRoom()).toMatchObject({ code: 'BCDF', roomInstanceId: NEWER_ID });
	});

	it.each<ClosedReason>(['replaced', 'unsupported_version'])(
		'%s keeps it: the seat is still ours and a reload comes back',
		async (reason) => {
			await inRoomThenClose(reason);
			expect(loadRecentRoom()).toMatchObject({ code: 'KT7P', roomInstanceId: ID });
		}
	);

	it('a terms refusal that the player declines clears it', async () => {
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		joined(f.created[0]);
		await tick();
		close(f.created[0], 'join_rejected', 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
		terms.dismiss();
		expect(await screen.findByRole('button', { name: 'Entrar a la sala' })).toBeTruthy();
		expect(slot()).toBeNull();
	});

	it('a terms refusal that is accepted joins again and remembers the room again', async () => {
		vi.spyOn(safetyApi, 'acceptTerms').mockResolvedValue(TERMS_OK);
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		close(f.created[0], 'join_rejected', 'terms_required');
		await waitFor(() => expect(terms.open).toBe(true));
		await terms.accept();
		await waitFor(() => expect(f.createSession).toHaveBeenCalledTimes(2));
		joined(f.created[1]);
		await waitFor(() => expect(loadRecentRoom()?.code).toBe('KT7P'));
	});

	it("does not erase another game's entry: only this room's own ending clears it", async () => {
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		joined(f.created[0]);
		await tick();
		rememberRecentRoom('BCDF', NEWER_ID); // another tab joined a different room meanwhile
		close(f.created[0], 'kicked');
		await tick();
		expect(loadRecentRoom()).toMatchObject({ code: 'BCDF', roomInstanceId: NEWER_ID });
	});

	it('does not erase the entry of a newer room that reuses the code', async () => {
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		joined(f.created[0]);
		await tick();
		rememberRecentRoom('KT7P', NEWER_ID);
		close(f.created[0], 'room_gone');
		await tick();
		expect(loadRecentRoom()).toMatchObject({ code: 'KT7P', roomInstanceId: NEWER_ID });
	});

	it('block and leave: any close of the room while the block is in flight clears it too', async () => {
		const f = factory();
		render(RoomRoute, { code: 'KT7P', createSession: f.createSession });
		joined(f.created[0]);
		await tick();
		blocks.expectRemoval('KT7P');
		close(f.created[0], 'kicked'); // the server removed the seat: 4002 reaches the client
		await tick();
		expect(slot()).toBeNull();
	});
});
