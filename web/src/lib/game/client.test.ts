import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CmdSetAnswer, PingInterval, Version, type View } from '$lib/protocol';
import {
	ANSWER_GAP_MS,
	CONNECT_TIMEOUT_MS,
	IDLE_TIMEOUT_MS,
	PROBE_TIMEOUT_MS,
	LEAVE_GRACE_MS,
	STABLE_MS
} from './client.svelte';
import { makeRound, makeSettings, makeView, player } from './fixtures';
import { FakeSocket, ME, harness, json, lobbyView, type Harness } from './net.testkit';

const T0 = new Date('2026-03-01T12:00:00Z');

beforeEach(() => {
	vi.useFakeTimers();
	vi.setSystemTime(T0);
});
afterEach(() => {
	vi.useRealTimers();
});

function playingView(answers: string[] = [], over: Partial<View> = {}): View {
	return makeView({
		phase: 'playing',
		youId: 'p1',
		hostId: 'p1',
		players: [ME, player('p2', 'Beto')],
		chooserId: 'p2',
		round: makeRound({
			number: 1,
			letter: 'F',
			participants: ['p1', 'p2'],
			answers: { p1: answers, p2: [] },
			filled: { p1: 0, p2: 0 }
		}),
		...over
	});
}

async function connected(over: Partial<Parameters<typeof harness>[0]> = {}, view = lobbyView()) {
	const h = harness(over);
	await h.join(view);
	return h;
}

describe('connect flow', () => {
	it('calls /api/session once, preflights the room, opens /ws and sends join first', async () => {
		const h = harness();
		expect(h.session.status).toBe('connecting');
		expect(h.sockets).toHaveLength(0);
		await h.settle();

		expect(h.calls.map((c) => c.url)).toEqual(['/api/session', '/api/rooms/KT7P']);
		for (const c of h.calls) expect(c.init?.credentials).toBe('same-origin');
		expect(h.sockets).toHaveLength(1);
		expect(h.sock().url).toBe('ws://localhost:5173/ws');
		expect(h.sock().raw).toHaveLength(0); // nothing before the socket opens

		h.sock().open();
		expect(h.sock().frames).toEqual([
			{
				t: 'join',
				v: Version,
				d: { code: 'KT7P', name: 'Ana', avatar: { emoji: '🍌', color: 'banana' } }
			}
		]);
		expect(h.session.status).toBe('connecting'); // until the first state
		expect(h.session.view).toBeNull();
	});

	it('uses wss: on https pages', async () => {
		const h = harness({ location: { protocol: 'https:', host: 'tuttifrutti.aresius.xyz' } });
		await h.settle();
		expect(h.sock().url).toBe('wss://tuttifrutti.aresius.xyz/ws');
	});

	it('is connected once the first state arrives and exposes the view', async () => {
		const h = await connected();
		expect(h.session.status).toBe('connected');
		expect(h.session.closedReason).toBeNull();
		expect(h.session.joinError).toBeNull();
		expect(h.session.view?.phase).toBe('lobby');
		expect(h.session.code).toBe('KT7P');
	});

	it('replaces the view on every state', async () => {
		const h = await connected();
		const next = lobbyView({ players: [ME, player('p2', 'Beto'), player('p3', 'Caro')] });
		h.sock().state(next);
		expect(h.session.view?.players).toHaveLength(3);
		expect(h.session.status).toBe('connected');
	});

	it('reads the profile on every attempt (a renamed player rejoins with the new name)', async () => {
		let name = 'Ana';
		const h = harness({ profile: () => ({ name, avatar: { emoji: '🍓', color: 'strawberry' } }) });
		await h.join();
		h.sock().serverClose(1006);
		name = 'Anita';
		await h.advance(500);
		h.sock().open();
		expect(h.sock().frames[0].d).toMatchObject({ name: 'Anita', avatar: { emoji: '🍓' } });
	});

	it('ignores garbage frames and unknown types', async () => {
		const h = await connected();
		const before = h.session.view;
		h.sock().receiveRaw('not json');
		h.sock().receiveRaw(42);
		h.sock().receiveRaw('"str"');
		h.sock().receiveRaw('null');
		h.sock().receiveRaw(JSON.stringify({ nope: 1 }));
		h.sock().receive('mystery', {});
		h.sock().receive('state', { code: 'KT7P', serverTime: 1 });
		h.sock().receive('event', { notAnEvent: true });
		h.sock().receive('error', {});
		expect(h.session.view).toBe(before);
		expect(h.session.status).toBe('connected');
		expect(h.session.events).toHaveLength(0);
		expect(h.session.lastError).toBeNull();
	});
});

describe('derived state', () => {
	it('mirrors FakeSession: players, me, host, chooser, participant', async () => {
		const gone = player('p3', 'Caro', { left: true });
		const view = playingView(['', ''], {
			players: [ME, player('p2', 'Beto'), gone],
			hostId: 'p2',
			chooserId: 'p1'
		});
		const h = await connected({}, view);
		const s = h.session;
		expect(s.players.map((p) => p.id)).toEqual(['p1', 'p2']);
		expect(s.me?.id).toBe('p1');
		expect(s.isHost).toBe(false);
		expect(s.isChooser).toBe(true);
		expect(s.isParticipant).toBe(true);

		h.sock().state(lobbyView());
		expect(s.isHost).toBe(true);
		expect(s.isChooser).toBe(false);
		expect(s.isParticipant).toBe(false);

		h.sock().state(lobbyView({ players: [player('p1', 'Ana', { left: true })] }));
		expect(s.me).toBeNull();
	});

	it('is empty before the first state', async () => {
		const h = harness();
		await h.settle();
		expect(h.session.players).toEqual([]);
		expect(h.session.me).toBeNull();
		expect(h.session.isHost).toBe(false);
		expect(h.session.isChooser).toBe(false);
		expect(h.session.isParticipant).toBe(false);
	});
});

describe('AI judge', () => {
	it('takes visibility only from state and defaults old fixtures to private', async () => {
		const h = harness();
		expect(h.session.visibility).toBe('private');
		await h.join(lobbyView());
		h.sock().receive('state', {
			code: 'KT7P',
			serverTime: Date.now(),
			view: lobbyView(),
			visibility: 'public'
		});
		expect(h.session.visibility).toBe('public');
		h.sock().state(lobbyView());
		expect(h.session.visibility).toBe('private');
	});
	it('canJudge is false before the first state and without the field (older fixtures)', async () => {
		const h = harness();
		await h.settle();
		expect(h.session.canJudge).toBe(false);
		await h.join(lobbyView());
		expect(h.session.canJudge).toBe(false);
	});

	it('follows the latest state frame', async () => {
		const h = await connected();
		h.sock().state(lobbyView(), Date.now(), 'KT7P', true);
		expect(h.session.canJudge).toBe(true);
		h.sock().state(lobbyView(), Date.now(), 'KT7P', false);
		expect(h.session.canJudge).toBe(false);
		h.sock().state(lobbyView(), Date.now(), 'KT7P', true);
		h.sock().state(lobbyView());
		expect(h.session.canJudge).toBe(false);
	});

	it('keeps the verdicts of the state and records judge_failed like any event', async () => {
		const view = lobbyView();
		view.round = makeRound({
			judgePending: [2],
			verdicts: [{ category: 1, player: 'p2', verdict: 'doubtful', reason: 'Not a country' }]
		});
		const h = await connected({}, view);
		expect(h.session.view?.round?.judgePending).toEqual([2]);
		expect(h.session.view?.round?.verdicts[0]).toMatchObject({ verdict: 'doubtful' });
		h.sock().receive('event', { type: 'judge_failed', reason: 'busy' });
		expect(h.session.events[0]).toMatchObject({ type: 'judge_failed', reason: 'busy' });
	});

	it.each([
		'not_host',
		'judge_pending',
		'already_judged',
		'nothing_to_judge',
		'judge_unavailable',
		'login_required'
	])('surfaces %s through lastError', async (code) => {
		const h = await connected();
		h.session.askJudge(1);
		h.sock().receive('error', { code, ref: 'ask_judge' });
		expect(h.session.lastError).toMatchObject({ code, ref: 'ask_judge' });
		expect(h.session.status).toBe('connected');
	});
});

describe('vote to stop', () => {
	const stopVote = {
		callerId: 'p2',
		endsAt: '2026-03-01T12:00:15Z',
		voters: ['p1'],
		yes: []
	};

	it('keeps the open vote of the state (and drops it with the next state)', async () => {
		const h = await connected({}, playingView(['', '', '']));
		const view = playingView(['', '', '']);
		view.round!.stopVote = stopVote;
		h.sock().state(view);
		expect(h.session.view?.round?.stopVote).toEqual(stopVote);
		h.sock().state(playingView(['', '', '']));
		expect(h.session.view?.round?.stopVote).toBeUndefined();
	});

	it('records stop_asked and stop_rejected like any event, and survives an unknown one', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.sock().receive('event', { type: 'stop_asked', playerId: 'p2', name: 'Beto' });
		h.sock().receive('event', {
			type: 'stop_rejected',
			playerId: 'p2',
			name: 'Beto',
			reason: 'declined'
		});
		h.sock().receive('event', { type: 'something_from_the_future', playerId: 'p2' });
		expect(h.session.events.map((e) => e.type)).toEqual([
			'stop_asked',
			'stop_rejected',
			'something_from_the_future'
		]);
		expect(h.session.events[1]).toMatchObject({ playerId: 'p2', reason: 'declined' });
		expect(h.session.status).toBe('connected');
		expect(h.session.closedReason).toBeNull();
	});

	it.each(['stop_vote_disabled', 'stop_vote_pending', 'no_stop_vote', 'stop_not_needed'])(
		'reports the %s error of the command it answers',
		async (code) => {
			const h = await connected({}, playingView(['', '', '']));
			h.session.askStop();
			h.sock().receive('error', { code, ref: 'ask_stop' });
			expect(h.session.lastError).toMatchObject({ code, ref: 'ask_stop' });
			expect(h.session.status).toBe('connected');
		}
	);
});

describe('events', () => {
	it('keeps the last 20 with local seq and arrival time', async () => {
		const h = await connected();
		for (let i = 0; i < 25; i++)
			h.sock().receive('event', { type: 'basta', playerId: 'p2', round: i });
		const events = h.session.events;
		expect(events).toHaveLength(20);
		expect(events[0].round).toBe(5);
		expect(events[19].round).toBe(24);
		const seqs = events.map((e) => e.seq);
		expect(seqs).toEqual([...seqs].sort((a, b) => a - b));
		expect(new Set(seqs).size).toBe(20);
		expect(events[0].at).toBe(Date.now());
	});

	it('closes as kicked when the kicked player is me, ignores others', async () => {
		const h = await connected();
		h.sock().receive('event', { type: 'kicked', playerId: 'p2' });
		expect(h.session.status).toBe('connected');
		expect(h.session.events.at(-1)?.type).toBe('kicked');
		h.sock().receive('event', { type: 'kicked', playerId: 'p1' });
		expect(h.session.status).toBe('closed');
		expect(h.session.closedReason).toBe('kicked');
	});

	it('closes as replaced on the replaced event', async () => {
		const h = await connected();
		h.sock().receive('event', { type: 'replaced' });
		expect(h.session.closedReason).toBe('replaced');
		expect(h.session.status).toBe('closed');
		expect(h.sock().closedWith?.code).toBe(1000);
	});

	it('keeps playing on server_restarting and reconnects from ~1 s after the 1012 close', async () => {
		const h = await connected();
		h.sock().receive('event', { type: 'server_restarting' });
		expect(h.session.status).toBe('connected');
		h.sock().serverClose(1012);
		expect(h.session.status).toBe('reconnecting');
		await h.advance(999);
		expect(h.roomCalls()).toBe(1);
		await h.advance(1);
		expect(h.roomCalls()).toBe(2);
	});
});

describe('errors', () => {
	it('records command errors with ref and an increasing seq', async () => {
		const h = await connected();
		h.sock().receive('error', { code: 'not_host', ref: 'kick' });
		const first = h.session.lastError;
		expect(first).toMatchObject({ code: 'not_host', ref: 'kick', at: Date.now() });
		h.sock().receive('error', { code: 'wrong_phase' });
		expect(h.session.lastError?.code).toBe('wrong_phase');
		expect(h.session.lastError!.seq).toBeGreaterThan(first!.seq);
		expect(h.session.status).toBe('connected');
	});

	it.each([
		['unsupported_version', 'unsupported_version'],
		['room_not_found', 'room_gone'],
		['room_closed', 'room_gone']
	])('closes for good on %s after joining', async (code, reason) => {
		const h = await connected();
		h.sock().receive('error', { code });
		expect(h.session.closedReason).toBe(reason);
		expect(h.session.lastError?.code).toBe(code);
		await h.advance(60_000);
		expect(h.sockets).toHaveLength(1);
	});

	it('does not close on other errors after joining (even with ref join)', async () => {
		const h = await connected();
		h.sock().receive('error', { code: 'already_joined', ref: 'join' });
		expect(h.session.status).toBe('connected');
		expect(h.session.lastError?.code).toBe('already_joined');
	});

	it.each(['lobby_full', 'invalid_name', 'invalid_avatar', 'wrong_phase', 'bad_message'])(
		'a rejected join (%s) closes as join_rejected with joinError',
		async (code) => {
			const h = harness();
			await h.settle();
			h.sock().open();
			h.sock().receive('error', { code, ref: 'join' });
			expect(h.session.status).toBe('closed');
			expect(h.session.closedReason).toBe('join_rejected');
			expect(h.session.joinError).toBe(code);
			expect(h.session.lastError).toMatchObject({ code, ref: 'join' });
			expect(h.sock().closedWith).not.toBeNull();
			await h.advance(60_000);
			expect(h.sockets).toHaveLength(1);
		}
	);

	it('a rejected join as kicked closes as kicked', async () => {
		const h = harness();
		await h.settle();
		h.sock().open();
		h.sock().receive('error', { code: 'kicked', ref: 'join' });
		expect(h.session.closedReason).toBe('kicked');
		expect(h.session.joinError).toBeNull();
	});

	it('an error before the first state also counts as a rejected join (no ref)', async () => {
		const h = harness();
		await h.settle();
		h.sock().open();
		h.sock().receive('error', { code: 'lobby_full' });
		expect(h.session.closedReason).toBe('join_rejected');
	});

	it('a rejected join on a RECONNECT is final too', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		await h.advance(500);
		h.sock().open();
		h.sock().receive('error', { code: 'lobby_full', ref: 'join' });
		expect(h.session.closedReason).toBe('join_rejected');
		expect(h.session.joinError).toBe('lobby_full');
	});

	it.each([
		['unsupported_version', 'unsupported_version'],
		['room_not_found', 'room_gone'],
		['room_closed', 'room_gone']
	])('a join answered with %s closes as %s', async (code, reason) => {
		const h = harness();
		await h.settle();
		h.sock().open();
		h.sock().receive('error', { code, ref: 'join' });
		expect(h.session.closedReason).toBe(reason);
		expect(h.session.joinError).toBeNull();
	});

	it.each(['server_busy', 'rate_limited', 'internal'])(
		'a join answered with %s keeps retrying quietly',
		async (code) => {
			const h = harness();
			await h.settle();
			h.sock().open();
			h.sock().receive('error', { code, ref: 'join' });
			expect(h.session.status).toBe('connecting');
			expect(h.session.closedReason).toBeNull();
			expect(h.session.lastError).toBeNull();
			expect(h.sock().closedWith).not.toBeNull();
			await h.advance(2000);
			expect(h.sockets).toHaveLength(2);
		}
	);
});

describe('close codes', () => {
	it.each([1006, 1001, 1012, 1008, 1009, 1000, 1011])(
		'%i reconnects and keeps the last view',
		async (code) => {
			const h = await connected();
			const view = h.session.view;
			h.sock().serverClose(code);
			expect(h.session.status).toBe('reconnecting');
			expect(h.session.view).toBe(view);
			expect(h.session.closedReason).toBeNull();
			await h.advance(2000);
			expect(h.sockets).toHaveLength(2);
			h.sock().open();
			h.sock().state(lobbyView());
			expect(h.session.status).toBe('connected');
		}
	);

	it.each([
		[4001, 'replaced'],
		[4002, 'kicked'],
		[4003, 'unsupported_version'],
		[4004, 'room_gone'],
		[4005, 'left']
	])('%i closes for good as %s', async (code, reason) => {
		const h = await connected();
		h.sock().serverClose(code);
		expect(h.session.status).toBe('closed');
		expect(h.session.closedReason).toBe(reason);
		expect(h.session.joinError).toBeNull();
		const requests = h.calls.length;
		await h.advance(120_000);
		expect(h.sockets).toHaveLength(1);
		expect(h.calls).toHaveLength(requests);
		expect(vi.getTimerCount()).toBe(0);
	});

	// The server contract: "MUST NOT reconnect after a 40xx code". A code this client
	// does not know must therefore end the session instead of hammering the server.
	it.each([4000, 4006, 4999])(
		'unknown application code %i closes for good as room_gone',
		async (code) => {
			const h = await connected();
			h.sock().serverClose(code);
			expect(h.session.status).toBe('closed');
			expect(h.session.closedReason).toBe('room_gone');
			await h.advance(120_000);
			expect(h.sockets).toHaveLength(1);
			expect(vi.getTimerCount()).toBe(0);
		}
	);

	it.each([3999, 5000])('code %i outside 40xx still reconnects', async (code) => {
		const h = await connected();
		h.sock().serverClose(code);
		expect(h.session.status).toBe('reconnecting');
		await h.advance(2000);
		expect(h.sockets).toHaveLength(2);
	});

	it('a close before the first state keeps status connecting', async () => {
		const h = harness();
		await h.settle();
		h.sock().open();
		h.sock().serverClose(1006);
		expect(h.session.status).toBe('connecting');
		await h.advance(500);
		expect(h.sockets).toHaveLength(2);
	});

	it('ignores late events of a socket that was already replaced', async () => {
		const h = await connected();
		const old = h.sock();
		old.serverClose(1006);
		await h.advance(500);
		const next = h.sock();
		expect(next).not.toBe(old);
		old.serverClose(4004);
		old.receive('event', { type: 'replaced' });
		old.state(lobbyView());
		expect(h.session.status).toBe('reconnecting');
		expect(h.session.events).toHaveLength(0);
	});
});

describe('backoff and preflight', () => {
	it('a 404 preflight closes for good as room_gone without opening a socket', async () => {
		const h = harness({ room: [json({ error: 'room_not_found' }, 404)] });
		await h.settle();
		expect(h.session.status).toBe('closed');
		expect(h.session.closedReason).toBe('room_gone');
		expect(h.sockets).toHaveLength(0);
		await h.advance(60_000);
		expect(h.calls).toHaveLength(2);
	});

	it('never gives up on 503 (starts at ~1 s and caps at 5 s)', async () => {
		const h = harness({ room: [json({ error: 'server_busy' }, 503)] });
		await h.settle();
		expect(h.session.status).toBe('connecting');
		let calls = h.roomCalls();
		expect(calls).toBe(1);
		for (const delay of [1000, 2000, 4000, 5000, 5000, 5000]) {
			await h.advance(delay - 1);
			expect(h.roomCalls()).toBe(calls);
			await h.advance(1);
			expect(h.roomCalls()).toBe(++calls);
		}
		expect(h.session.status).toBe('connecting');
		expect(h.sockets).toHaveLength(0);
		expect(h.calls.filter((c) => c.url === '/api/session')).toHaveLength(1);
	});

	it.each([502, 504])('keeps retrying on %i', async (status) => {
		const h = harness({ room: [new Response('bad gateway', { status }), json({ code: 'KT7P' })] });
		await h.settle();
		expect(h.sockets).toHaveLength(0);
		await h.advance(1000);
		expect(h.sockets).toHaveLength(1);
	});

	it('uses 500 ms, 1 s, 2 s, 4 s, 5 s for network errors (no server_busy)', async () => {
		const h = harness({ room: [new TypeError('offline')] });
		await h.settle();
		let calls = h.roomCalls();
		for (const delay of [500, 1000, 2000, 4000, 5000, 5000]) {
			await h.advance(delay - 1);
			expect(h.roomCalls()).toBe(calls);
			await h.advance(1);
			expect(h.roomCalls()).toBe(++calls);
		}
		expect(h.session.status).toBe('connecting');
	});

	it('applies full jitter (random scales the delay) with a 100 ms floor', async () => {
		const h = harness({ room: [new TypeError('offline')], random: () => 0 });
		await h.settle();
		await h.advance(99);
		expect(h.roomCalls()).toBe(1);
		await h.advance(1);
		expect(h.roomCalls()).toBe(2);

		const half = harness({ room: [new TypeError('offline')], random: () => 0.5 });
		await half.settle();
		await half.advance(249);
		expect(half.roomCalls()).toBe(1);
		await half.advance(1);
		expect(half.roomCalls()).toBe(2);
	});

	it('429 honours Retry-After (capped at 10 s) and keeps going', async () => {
		const h = harness({
			room: [json({ error: 'rate_limited' }, 429, { 'Retry-After': '3' }), json({ code: 'KT7P' })]
		});
		await h.settle();
		await h.advance(2999);
		expect(h.sockets).toHaveLength(0);
		await h.advance(1);
		expect(h.sockets).toHaveLength(1);

		const big = harness({
			room: [json({ error: 'rate_limited' }, 429, { 'Retry-After': '600' }), json({})]
		});
		await big.settle();
		await big.advance(9999);
		expect(big.sockets).toHaveLength(0);
		await big.advance(1);
		expect(big.sockets).toHaveLength(1);
	});

	it('retries a failing /api/session and calls it again until it works', async () => {
		const h = harness({ session: [new TypeError('offline'), json({ guest: true })] });
		await h.settle();
		expect(h.calls.map((c) => c.url)).toEqual(['/api/session']);
		await h.advance(500);
		expect(h.calls.map((c) => c.url)).toEqual(['/api/session', '/api/session', '/api/rooms/KT7P']);
		expect(h.sockets).toHaveLength(1);
	});

	it('does not let a non-transient /api/session failure block the room', async () => {
		const h = harness({ session: [json({ error: 'not_found' }, 404)] });
		await h.settle();
		expect(h.sockets).toHaveLength(1);
	});

	it('runs the preflight again on every reconnect, but /api/session only once', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		await h.advance(500);
		h.sock().serverClose(1006);
		await h.advance(1000);
		expect(h.calls.filter((c) => c.url === '/api/session')).toHaveLength(1);
		expect(h.roomCalls()).toBe(3);
	});

	it('a room that disappears while reconnecting ends as room_gone', async () => {
		const h = await connected();
		h.replies.room = [json({ error: 'room_not_found' }, 404)];
		h.sock().serverClose(1006);
		await h.advance(500);
		expect(h.session.closedReason).toBe('room_gone');
		expect(h.session.status).toBe('closed');
	});

	it('resets the backoff after a join that held', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		await h.advance(500); // attempt 0: 500
		h.sock().open();
		h.sock().state(lobbyView());
		await h.advance(STABLE_MS);
		h.sock().serverClose(1006);
		await h.advance(499);
		expect(h.sockets).toHaveLength(2);
		await h.advance(1);
		expect(h.sockets).toHaveLength(3); // 500 again, not 1000
	});

	it('does not reset the backoff after a connection that flapped right away', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		await h.advance(500);
		h.sock().open();
		h.sock().state(lobbyView());
		await h.advance(500); // dies after 0.5 s: unstable
		h.sock().serverClose(1006);
		await h.advance(999);
		expect(h.sockets).toHaveLength(2);
		await h.advance(1);
		expect(h.sockets).toHaveLength(3); // second step: 1000
	});

	it('abandons a socket that never opens', async () => {
		const h = harness();
		await h.settle();
		await h.advance(CONNECT_TIMEOUT_MS - 1);
		expect(h.sockets).toHaveLength(1);
		await h.advance(1);
		expect(h.sock().closedWith).not.toBeNull();
		expect(h.session.status).toBe('connecting');
		await h.advance(500);
		expect(h.sockets).toHaveLength(2);
	});

	it('retries when the socket cannot even be created', async () => {
		let fail = true;
		const h = harness({
			createSocket: (url) => {
				if (fail) throw new Error('SecurityError');
				return new FakeSocket(url);
			}
		});
		await h.settle();
		expect(h.session.status).toBe('connecting');
		fail = false;
		await h.advance(500);
		expect(h.calls.filter((c) => c.url.startsWith('/api/rooms/'))).toHaveLength(2);
		expect(h.sockets).toHaveLength(0); // created outside the harness' factory
	});
});

describe('heartbeat and dead connections', () => {
	it('pings every 20 s while open', async () => {
		const h = await connected();
		expect(PingInterval).toBe(20_000);
		await h.advance(PingInterval - 1);
		expect(h.sock().types).toEqual(['join']);
		await h.advance(1);
		expect(h.sock().types).toEqual(['join', 'ping']);
		h.sock().receive('pong', { serverTime: Date.now() });
		await h.advance(PingInterval);
		expect(h.sock().frames.filter((f) => f.t === 'ping')).toHaveLength(2);
		expect(h.sock().frames.at(-1)).toEqual({ t: 'ping', v: Version });
	});

	it('reconnects when nothing at all arrives for 45 s', async () => {
		const h = await connected();
		await h.advance(IDLE_TIMEOUT_MS - 1);
		expect(h.session.status).toBe('connected');
		await h.advance(1);
		expect(h.session.status).toBe('reconnecting');
		expect(h.sockets[0].closedWith).not.toBeNull();
		await h.advance(500);
		expect(h.sockets).toHaveLength(2);
	});

	it('any frame (pong, state, event) keeps the connection alive', async () => {
		const h = await connected();
		for (let i = 0; i < 6; i++) {
			await h.advance(30_000);
			h.sock().receive(
				i % 2 ? 'pong' : 'event',
				i % 2 ? { serverTime: Date.now() } : { type: 'basta' }
			);
		}
		expect(h.session.status).toBe('connected');
		expect(h.sockets).toHaveLength(1);
	});

	it('stops pinging and watching once the socket is gone', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		expect(vi.getTimerCount()).toBe(1); // only the retry timer
	});
});

describe('wake-ups: online and visibilitychange', () => {
	it('reconnects immediately on `online` while waiting for the backoff', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		h.sock().serverClose(1006);
		await h.advance(1000);
		h.sock().serverClose(1006);
		expect(h.sockets).toHaveLength(2);
		h.win.emit('online');
		await h.settle();
		expect(h.sockets).toHaveLength(3);
		expect(h.session.status).toBe('reconnecting');
	});

	it('reconnects immediately when the tab becomes visible and resets the backoff', async () => {
		const h = harness({ room: [json({ error: 'server_busy' }, 503)] });
		await h.settle();
		await h.advance(1000);
		await h.advance(2000); // now waiting for a 4 s retry
		expect(h.roomCalls()).toBe(3);
		h.replies.room = [json({ code: 'KT7P' })];
		h.doc.visibilityState = 'visible';
		h.doc.emit('visibilitychange');
		await h.settle();
		expect(h.roomCalls()).toBe(4);
		expect(h.sockets).toHaveLength(1);
	});

	it('ignores visibilitychange while hidden', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		h.doc.visibilityState = 'hidden';
		h.doc.emit('visibilitychange');
		await h.settle();
		expect(h.sockets).toHaveLength(1);
		await h.advance(500);
		expect(h.sockets).toHaveLength(2); // the normal backoff still runs
	});

	it('connected: probes with a ping and reconnects when nothing answers', async () => {
		const h = await connected();
		await h.advance(10_000);
		h.doc.emit('visibilitychange');
		expect(h.sock().types.at(-1)).toBe('ping');
		await h.advance(PROBE_TIMEOUT_MS);
		expect(h.session.status).toBe('reconnecting');
		await h.advance(500);
		expect(h.sockets).toHaveLength(2);
	});

	it('connected: a pong within the probe window keeps the socket', async () => {
		const h = await connected();
		h.win.emit('online');
		h.sock().receive('pong', { serverTime: Date.now() });
		await h.advance(PROBE_TIMEOUT_MS * 2);
		expect(h.session.status).toBe('connected');
		expect(h.sockets).toHaveLength(1);
	});

	it('connected after a long freeze: reconnects at once', async () => {
		const h = await connected();
		vi.setSystemTime(Date.now() + IDLE_TIMEOUT_MS + 1000); // timers did not run while frozen
		h.doc.emit('visibilitychange');
		await h.settle();
		expect(h.sockets).toHaveLength(2);
	});

	it('does nothing once closed or destroyed', async () => {
		const h = await connected();
		h.sock().serverClose(4004);
		h.win.emit('online');
		h.doc.emit('visibilitychange');
		await h.settle();
		expect(h.sockets).toHaveLength(1);
		expect(h.win.count() + h.doc.count()).toBe(0);
	});
});

describe('commands', () => {
	const settings = makeSettings({ rounds: 3, timeLimitSeconds: 60 });
	const cases: [string, (h: Harness) => void, string, unknown][] = [
		['updateSettings', (h) => h.session.updateSettings(settings), 'update_settings', { settings }],
		['kick', (h) => h.session.kick('p2'), 'kick', { playerId: 'p2' }],
		['startGame', (h) => h.session.startGame(), 'start_game', undefined],
		['stopLetter', (h) => h.session.stopLetter(), 'stop_letter', undefined],
		['basta', (h) => h.session.basta(), 'basta', undefined],
		['askStop', (h) => h.session.askStop(), 'ask_stop', undefined],
		['voteStop yes', (h) => h.session.voteStop(true), 'vote_stop', { yes: true }],
		['voteStop no', (h) => h.session.voteStop(false), 'vote_stop', { yes: false }],
		[
			'voteReject',
			(h) => h.session.voteReject(2, 'p2'),
			'vote_reject',
			{ category: 2, targetPlayer: 'p2' }
		],
		[
			'voteReject withdraw',
			(h) => h.session.voteReject(2, 'p2', true),
			'vote_reject',
			{ category: 2, targetPlayer: 'p2', withdraw: true }
		],
		[
			'proposeMerge',
			(h) => h.session.proposeMerge(1, 'p2', 'p3'),
			'propose_merge',
			{ category: 1, playerA: 'p2', playerB: 'p3' }
		],
		['voteMerge', (h) => h.session.voteMerge(7, true), 'vote_merge', { proposalId: 7, yes: true }],
		['reviewNav', (h) => h.session.reviewNav(4), 'review_nav', { category: 4 }],
		['askJudge', (h) => h.session.askJudge(3), 'ask_judge', { category: 3 }],
		['finishReview', (h) => h.session.finishReview(), 'finish_review', undefined],
		['nextRound', (h) => h.session.nextRound(), 'next_round', undefined],
		['endGame', (h) => h.session.endGame(), 'end_game', undefined],
		['rematch', (h) => h.session.rematch(), 'rematch', undefined]
	];

	it.each(cases)('%s sends the right frame', async (_name, call, t, d) => {
		const h = await connected();
		const before = h.sock().raw.length;
		call(h);
		expect(h.sock().raw).toHaveLength(before + 1);
		const frame = h.sock().frames.at(-1)!;
		expect(frame.t).toBe(t);
		expect(frame.v).toBe(Version);
		if (d === undefined) expect('d' in frame).toBe(false);
		else expect(frame.d).toEqual(d);
		expect(h.session.lastError).toBeNull();
	});

	it.each(cases)('%s while reconnecting is dropped with not_connected', async (_name, call, t) => {
		const h = await connected();
		h.sock().serverClose(1006);
		const sent = h.sockets[0].raw.length;
		call(h);
		expect(h.sockets[0].raw).toHaveLength(sent);
		expect(h.session.lastError).toMatchObject({ code: 'not_connected', ref: t });
	});

	it('commands before the first state are dropped with not_connected', async () => {
		const h = harness();
		await h.settle();
		h.sock().open();
		h.session.startGame();
		expect(h.sock().types).toEqual(['join']);
		expect(h.session.lastError).toMatchObject({ code: 'not_connected', ref: 'start_game' });
	});

	it('leave() sends leave, closes as left and stops for good', async () => {
		const h = await connected();
		h.session.leave();
		expect(h.sock().types).toEqual(['join', 'leave']);
		expect(h.session.status).toBe('closed');
		expect(h.session.closedReason).toBe('left');
		expect(h.sock().closedWith).not.toBeNull();
		await h.advance(120_000);
		expect(h.sockets).toHaveLength(1);
		expect(vi.getTimerCount()).toBe(0);
		h.session.leave(); // idempotent
		expect(h.session.closedReason).toBe('left');
	});

	describe('leave() while offline', () => {
		it('keeps reconnecting and delivers the leave as soon as the join succeeds', async () => {
			const h = await connected();
			h.sock().serverClose(1006);
			h.session.leave();
			// not closed yet: the server has not heard about it
			expect(h.session.status).toBe('reconnecting');
			expect(h.session.closedReason).toBeNull();
			await h.advance(2000);
			expect(h.sockets).toHaveLength(2);
			h.sock().open();
			h.sock().state(lobbyView());
			expect(h.sock().types).toEqual(['join', 'leave']);
			expect(h.session.closedReason).toBe('left');
			expect(h.session.status).toBe('closed');
			await h.advance(120_000);
			expect(h.sockets).toHaveLength(2);
			expect(vi.getTimerCount()).toBe(0);
		});

		it('leaves locally after the grace when the network stays down', async () => {
			const h = await connected();
			h.sock().serverClose(1006);
			h.session.leave();
			h.session.leave(); // idempotent: does not restart the grace
			await h.advance(LEAVE_GRACE_MS - 100);
			expect(h.session.closedReason).toBeNull();
			await h.advance(200);
			expect(h.session.closedReason).toBe('left');
			expect(h.session.status).toBe('closed');
			await h.advance(120_000);
			expect(vi.getTimerCount()).toBe(0);
		});

		it('does not send queued answers before the leave', async () => {
			const h = await connected({}, playingView(['', '', '']));
			h.sock().serverClose(1006);
			h.session.setAnswer(0, 'abc');
			h.session.leave();
			await h.advance(2000);
			h.sock().open();
			h.sock().state(playingView(['', '', '']));
			expect(h.sock().types).toEqual(['join', 'leave']);
		});

		it('a leave before any state arrived (never seated) closes at once', async () => {
			const h = harness();
			await h.settle();
			h.session.leave();
			expect(h.session.closedReason).toBe('left');
			expect(h.session.status).toBe('closed');
		});

		it('another terminal event during the grace wins and cancels the timer', async () => {
			const h = await connected();
			h.sock().serverClose(1006);
			h.session.leave();
			await h.advance(2000);
			h.sock().open();
			h.sock().serverClose(4002);
			expect(h.session.closedReason).toBe('kicked');
			await h.advance(120_000);
			expect(vi.getTimerCount()).toBe(0);
		});
	});

	it('commands after closing report not_connected', async () => {
		const h = await connected();
		h.sock().serverClose(4002);
		h.session.startGame();
		expect(h.session.lastError?.code).toBe('not_connected');
	});

	it('after leave() a closed session does not change reason on later events', async () => {
		const h = await connected();
		h.session.leave();
		h.sock().serverClose(4005);
		expect(h.session.closedReason).toBe('left');
	});
});

describe('answers', () => {
	const sentAnswers = (h: Harness) =>
		h
			.sock()
			.frames.filter((f) => f.t === CmdSetAnswer)
			.map((f) => f.d);

	it('sends the first change right away and coalesces a burst into one trailing frame', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'F');
		expect(sentAnswers(h)).toEqual([{ category: 0, text: 'F' }]);
		await h.advance(30);
		h.session.setAnswer(0, 'Fr');
		await h.advance(30);
		h.session.setAnswer(0, 'Fra');
		expect(sentAnswers(h)).toHaveLength(1);
		await h.advance(ANSWER_GAP_MS - 60 - 1);
		expect(sentAnswers(h)).toHaveLength(1);
		await h.advance(1);
		expect(sentAnswers(h)).toEqual([
			{ category: 0, text: 'F' },
			{ category: 0, text: 'Fra' }
		]);
		await h.advance(1000);
		expect(sentAnswers(h)).toHaveLength(2);
		expect(vi.getTimerCount()).toBe(2); // ping + watchdog only
	});

	it('limits each category to one frame per 100 ms, independently', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'a');
		h.session.setAnswer(1, 'b');
		expect(sentAnswers(h)).toHaveLength(2);
		await h.advance(150);
		h.session.setAnswer(0, 'ab');
		expect(sentAnswers(h)).toHaveLength(3); // the gap elapsed: immediate again
		h.session.setAnswer(0, 'abc');
		await h.advance(99);
		expect(sentAnswers(h)).toHaveLength(3);
		await h.advance(1);
		expect(sentAnswers(h).at(-1)).toEqual({ category: 0, text: 'abc' });
	});

	it('basta() flushes pending answers first and is never delayed', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'F');
		h.session.setAnswer(0, 'Fa');
		h.session.setAnswer(1, 'Fb');
		h.session.setAnswer(1, 'Fbb');
		const before = h.sock().raw.length;
		h.session.basta();
		const frames = h.sock().frames.slice(before);
		expect(frames.map((f) => f.t)).toEqual(['set_answer', 'set_answer', 'basta']);
		expect(frames.slice(0, 2).map((f) => f.d)).toEqual([
			{ category: 0, text: 'Fa' },
			{ category: 1, text: 'Fbb' }
		]);
		await h.advance(500);
		expect(h.sock().raw).toHaveLength(before + 3);
		expect(h.sock().types.filter((t) => t === 'basta')).toHaveLength(1);
	});

	it('askStop() flushes pending answers first, like basta, and is sent once', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'F');
		h.session.setAnswer(0, 'Fa');
		h.session.setAnswer(2, 'Fo');
		h.session.setAnswer(2, 'Foc');
		const before = h.sock().raw.length;
		h.session.askStop();
		const frames = h.sock().frames.slice(before);
		expect(frames.map((f) => f.t)).toEqual(['set_answer', 'set_answer', 'ask_stop']);
		expect(frames.slice(0, 2).map((f) => f.d)).toEqual([
			{ category: 0, text: 'Fa' },
			{ category: 2, text: 'Foc' }
		]);
		await h.advance(500);
		expect(h.sock().types.filter((t) => t === 'ask_stop')).toHaveLength(1);
	});

	it('askStop() and voteStop() offline report not_connected and send nothing', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.sock().serverClose(1006);
		const sent = h.sockets[0].raw.length;
		h.session.askStop();
		expect(h.session.lastError).toMatchObject({ code: 'not_connected', ref: 'ask_stop' });
		h.session.voteStop(true);
		expect(h.session.lastError).toMatchObject({ code: 'not_connected', ref: 'vote_stop' });
		expect(h.sockets[0].raw).toHaveLength(sent);
	});

	it('other commands are not delayed by answer throttling', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'a');
		h.session.setAnswer(0, 'ab');
		h.session.stopLetter();
		expect(h.sock().types.at(-1)).toBe('stop_letter');
	});

	it('skips unchanged values (own sends and the server view)', async () => {
		const h = await connected({}, playingView(['Fresa', '', '']));
		h.session.setAnswer(0, 'Fresa'); // the server already has it
		expect(sentAnswers(h)).toHaveLength(0);
		h.session.setAnswer(1, 'Fiyi');
		await h.advance(500);
		h.session.setAnswer(1, 'Fiyi');
		expect(sentAnswers(h)).toEqual([{ category: 1, text: 'Fiyi' }]);
		h.session.setAnswer(1, ''); // clearing is a change
		expect(sentAnswers(h).at(-1)).toEqual({ category: 1, text: '' });
	});

	it('forgets what it sent when a new round starts', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'Fa');
		await h.advance(500);
		h.sock().state(
			playingView(['', '', ''], {
				round: makeRound({
					number: 2,
					letter: 'B',
					participants: ['p1', 'p2'],
					answers: { p1: [] }
				})
			})
		);
		h.session.setAnswer(0, 'Fa');
		expect(sentAnswers(h)).toHaveLength(2);
	});

	it('keeps the latest text per category while offline and flushes after the next join', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.sock().serverClose(1006);
		h.session.setAnswer(0, 'Fr');
		h.session.setAnswer(0, 'Fra');
		h.session.setAnswer(2, 'Fo');
		expect(h.sockets[0].frames.filter((f) => f.t === CmdSetAnswer)).toHaveLength(0);
		expect(h.session.lastError).toBeNull();
		await h.advance(500);
		const s = h.sock();
		s.open();
		expect(s.types).toEqual(['join']);
		s.state(playingView(['', '', '']));
		expect(s.frames.slice(1)).toEqual([
			{ t: 'set_answer', v: Version, d: { category: 0, text: 'Fra' } },
			{ t: 'set_answer', v: Version, d: { category: 2, text: 'Fo' } }
		]);
	});

	it('dedupes an offline answer the server already has', async () => {
		const h = await connected({}, playingView(['Fra', '', '']));
		h.sock().serverClose(1006);
		h.session.setAnswer(0, 'Fra');
		await h.advance(500);
		h.sock().open();
		h.sock().state(playingView(['Fra', '', '']));
		expect(h.sock().frames.filter((f) => f.t === CmdSetAnswer)).toHaveLength(0);
	});

	it('drops offline answers that became stale (new round, other phase, not a participant)', async () => {
		const stale = async (next: View) => {
			const h = await connected({}, playingView(['', '', '']));
			h.sock().serverClose(1006);
			h.session.setAnswer(0, 'Fr');
			await h.advance(500);
			h.sock().open();
			h.sock().state(next);
			return h.sock().frames.filter((f) => f.t === CmdSetAnswer);
		};
		const round2 = playingView([], {
			round: makeRound({ number: 2, letter: 'B', participants: ['p1', 'p2'], answers: { p1: [] } })
		});
		expect(await stale(round2)).toHaveLength(0);
		expect(await stale(playingView([], { phase: 'review' }))).toHaveLength(0);
		expect(await stale(lobbyView())).toHaveLength(0);
		const spectator = playingView([], {
			round: makeRound({ number: 1, letter: 'F', participants: ['p2'], answers: {} })
		});
		expect(await stale(spectator)).toHaveLength(0);
	});

	it('before any state (unknown round) a typed answer is kept and sent in grace too', async () => {
		const h = harness();
		await h.settle();
		h.session.setAnswer(0, 'Fa');
		h.sock().open();
		h.sock().state(playingView(['', '', ''], { phase: 'grace' }));
		expect(sentAnswers(h)).toEqual([{ category: 0, text: 'Fa' }]);
	});

	it('does not send a pending answer through a dead socket', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.setAnswer(0, 'a');
		h.session.setAnswer(0, 'ab'); // trailing timer armed
		h.sock().serverClose(1006);
		await h.advance(100);
		expect(sentAnswers(h)).toHaveLength(1);
	});

	it('ignores answers after the session is closed', async () => {
		const h = await connected({}, playingView(['', '', '']));
		h.session.leave();
		h.session.setAnswer(0, 'x');
		expect(h.session.lastError).toBeNull();
	});
});

describe('serverNow', () => {
	it('is the local clock before any sample', async () => {
		const h = harness();
		expect(h.session.serverNow()).toBe(Date.now());
	});

	it('derives the offset from the join round-trip (symmetric latency)', async () => {
		const h = harness();
		await h.settle();
		const s = h.sock();
		s.open();
		await h.advance(100); // 100 ms round trip
		s.state(lobbyView(), Date.now() - 50 + 5000); // server is 5 s ahead, 50 ms one way
		expect(h.session.serverNow()).toBe(Date.now() + 5000);
		await h.advance(1234);
		expect(h.session.serverNow()).toBe(Date.now() + 5000);
	});

	it('keeps the lowest round-trip sample among pongs', async () => {
		const h = await connected({}, lobbyView());
		// join sample: rtt 0, offset 0 (serverTime === now)
		expect(h.session.serverNow()).toBe(Date.now());
		await h.advance(PingInterval);
		await h.advance(400);
		h.sock().receive('pong', { serverTime: Date.now() - 200 + 3000 }); // rtt 400: offset 3000
		expect(h.session.serverNow()).toBe(Date.now()); // the join sample (rtt 0) still wins
	});

	it('a better sample replaces the offset', async () => {
		const h = harness();
		await h.settle();
		h.sock().open();
		await h.advance(200);
		h.sock().state(lobbyView(), Date.now() - 100 + 1000); // rtt 200, offset 1000
		expect(h.session.serverNow()).toBe(Date.now() + 1000);
		await h.advance(PingInterval - 200);
		await h.advance(20);
		h.sock().receive('pong', { serverTime: Date.now() - 10 + 1500 }); // rtt 20, offset 1500
		expect(h.session.serverNow()).toBe(Date.now() + 1500);
	});

	it('ignores a pong nobody asked for and a non-numeric serverTime', async () => {
		const h = await connected();
		h.sock().receive('pong', { serverTime: Date.now() + 99_999 });
		expect(h.session.serverNow()).toBe(Date.now());
		await h.advance(PingInterval);
		h.sock().receive('pong', { serverTime: 'nope' });
		h.sock().receive('pong');
		expect(h.session.serverNow()).toBe(Date.now());
	});

	it('forgets old samples after a reconnect', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		await h.advance(500);
		h.sock().open();
		h.sock().state(lobbyView(), Date.now() + 7000);
		expect(h.session.serverNow()).toBe(Date.now() + 7000);
	});
});

describe('destroy', () => {
	it('closes the socket, clears timers and listeners, and is idempotent', async () => {
		const h = await connected();
		h.session.setAnswer(0, 'a');
		expect(h.win.count() + h.doc.count()).toBe(2);
		h.session.destroy();
		expect(h.sock().closedWith).not.toBeNull();
		expect(vi.getTimerCount()).toBe(0);
		expect(h.win.count() + h.doc.count()).toBe(0);
		h.session.destroy();
		await h.advance(120_000);
		expect(h.sockets).toHaveLength(1);
		expect(h.session.closedReason).toBeNull(); // does not leave nor close the room
		h.session.startGame();
		expect(h.session.lastError).toBeNull();
		h.session.leave();
		expect(h.session.closedReason).toBeNull();
	});

	it('cancels an attempt in flight', async () => {
		const h = harness();
		h.session.destroy();
		await h.settle();
		expect(h.sockets).toHaveLength(0);
		expect(vi.getTimerCount()).toBe(0);
	});

	it('cancels a pending retry', async () => {
		const h = harness({ room: [new TypeError('offline')] });
		await h.settle();
		expect(vi.getTimerCount()).toBe(1);
		h.session.destroy();
		expect(vi.getTimerCount()).toBe(0);
		await h.advance(60_000);
		expect(h.roomCalls()).toBe(1);
	});

	it('ignores frames from a destroyed session', async () => {
		const h = await connected();
		const s = h.sock();
		h.session.destroy();
		s.state(lobbyView({ players: [ME] }));
		s.serverClose(4004);
		expect(h.session.view?.players).toHaveLength(2);
		expect(h.session.status).toBe('connected');
	});

	it('reconnect attempts that resolve after destroy do nothing', async () => {
		const h = await connected();
		h.sock().serverClose(1006);
		h.win.emit('online'); // starts an attempt right now: its preflight is still in flight
		h.session.destroy();
		await h.settle();
		expect(h.sockets).toHaveLength(1);
	});
});

describe('flood protection of the connect loop', () => {
	it('wake-ups while the preflight is in flight restart one attempt, not many', async () => {
		const h = harness({ room: [new TypeError('offline'), json({})] });
		await h.settle();
		h.win.emit('online');
		h.win.emit('online');
		await h.settle();
		expect(h.sockets.length).toBeLessThanOrEqual(1);
	});
});
