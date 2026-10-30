import { describe, expect, it } from 'vitest';
import { ApiError, type RoomInfo } from '$lib/api';
import type { RecentRoom } from './recent';
import { verdictOf } from './verdict';

const recent: RecentRoom = {
	code: 'KT7P',
	roomInstanceId: '01020304-0506-4708-890a-0b0c0d0e0f10',
	lastJoinedAt: 1
};

const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	visibility: 'private',
	roomInstanceId: recent.roomInstanceId,
	...over
});

describe('what Home does with the remembered room', () => {
	it('offers a room that is the same instance', () => {
		expect(verdictOf(recent, { info: info() })).toBe('offer');
	});

	it.each([
		['full', info({ players: 10, joinable: false })],
		['finished', info({ phase: 'final', joinable: false })],
		['in progress', info({ phase: 'playing', joinable: false })],
		['draining', info({ joinable: false })]
	])(
		'offers a %s room too: not joinable is about NEW seats, the server decides at the join',
		(_n, i) => {
			expect(verdictOf(recent, { info: i })).toBe('offer');
		}
	);

	it('forgets a code that now belongs to another room (different instance id)', () => {
		const other = '11121314-1516-4718-991a-1b1c1d1e1f20';
		expect(verdictOf(recent, { info: info({ roomInstanceId: other }) })).toBe('forget');
	});

	it('offers when the server does not report an instance id (an older release): the join decides', () => {
		expect(verdictOf(recent, { info: info({ roomInstanceId: '' }) })).toBe('offer');
		const old = { ...info() } as Partial<RoomInfo>;
		delete old.roomInstanceId;
		expect(verdictOf(recent, { info: old as RoomInfo })).toBe('offer');
	});

	it.each([
		['404', new ApiError(404, 'room_not_found')],
		['a room_not_found code', new ApiError(410, 'room_not_found')],
		['join_unavailable (a block or a restriction)', new ApiError(403, 'join_unavailable')]
	])('forgets on %s', (_name, error) => {
		expect(verdictOf(recent, { error })).toBe('forget');
	});

	it.each([
		['offline', new ApiError(0, 'network')],
		['busy / draining', new ApiError(503, 'server_busy')],
		['rate limited', new ApiError(429, 'rate_limited')],
		['an unexpected 400', new ApiError(400, 'unknown')],
		['an aborted check', new DOMException('aborted', 'AbortError')],
		['anything else', new Error('boom')]
	])('keeps the card when the check fails: %s', (_name, error) => {
		expect(verdictOf(recent, { error })).toBe('offer');
	});
});
