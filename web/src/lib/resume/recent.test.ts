import { describe, expect, it } from 'vitest';
import {
	RECENT_ROOM_KEY,
	RECENT_ROOM_TTL_MS,
	clearRecentRoom,
	loadRecentRoom,
	rememberRecentRoom
} from './recent';

const ID_A = '01020304-0506-4708-890a-0b0c0d0e0f10';
const ID_B = '11121314-1516-4718-991a-1b1c1d1e1f20';
const NOW = 1_800_000_000_000;

function memory(initial: Record<string, string> = {}) {
	const data = new Map<string, string>(Object.entries(initial));
	return {
		data,
		getItem: (k: string) => data.get(k) ?? null,
		setItem: (k: string, v: string) => void data.set(k, v),
		removeItem: (k: string) => void data.delete(k)
	};
}

const broken = {
	getItem: () => {
		throw new Error('blocked');
	},
	setItem: () => {
		throw new Error('blocked');
	},
	removeItem: () => {
		throw new Error('blocked');
	}
};

describe('recent room (localStorage tf.recentRoom)', () => {
	it('stores exactly the code, the instance id and the join time, and reads them back', () => {
		const s = memory();
		rememberRecentRoom('kt7p', ID_A, NOW, s);
		expect([...s.data.keys()]).toEqual([RECENT_ROOM_KEY]);
		expect(JSON.parse(s.data.get(RECENT_ROOM_KEY)!)).toEqual({
			code: 'KT7P',
			roomInstanceId: ID_A,
			lastJoinedAt: NOW
		});
		expect(loadRecentRoom(NOW + 1000, s)).toEqual({
			code: 'KT7P',
			roomInstanceId: ID_A,
			lastJoinedAt: NOW
		});
	});

	it('keeps one slot: the next join replaces the previous room', () => {
		const s = memory();
		rememberRecentRoom('KT7P', ID_A, NOW, s);
		rememberRecentRoom('BCDF', ID_B, NOW + 5, s);
		expect(s.data.size).toBe(1);
		expect(loadRecentRoom(NOW + 10, s)).toEqual({
			code: 'BCDF',
			roomInstanceId: ID_B,
			lastJoinedAt: NOW + 5
		});
	});

	it('refreshes the time when the same room is joined again', () => {
		const s = memory();
		rememberRecentRoom('KT7P', ID_A, NOW, s);
		rememberRecentRoom('KT7P', ID_A, NOW + 3_600_000, s);
		expect(loadRecentRoom(NOW + 3_600_001, s)?.lastJoinedAt).toBe(NOW + 3_600_000);
	});

	it.each([
		['a code that is too short', 'KT7', ID_A],
		['a code with a vowel', 'KA7P', ID_A],
		['no code', '', ID_A],
		['an instance id that is not a UUID', 'KT7P', 'room-instance-1'],
		['an empty instance id', 'KT7P', '']
	])('writes nothing for %s', (_name, code, id) => {
		const s = memory();
		rememberRecentRoom(code, id, NOW, s);
		expect(s.data.size).toBe(0);
	});

	it('writes nothing for a clock that is not a number', () => {
		const s = memory();
		rememberRecentRoom('KT7P', ID_A, Number.NaN, s);
		expect(s.data.size).toBe(0);
	});

	describe('expiry (24 h, checked on read)', () => {
		it('is still there just under 24 h and gone at 24 h, and the expired slot is removed', () => {
			const s = memory();
			rememberRecentRoom('KT7P', ID_A, NOW, s);
			expect(RECENT_ROOM_TTL_MS).toBe(24 * 60 * 60 * 1000);
			expect(loadRecentRoom(NOW + RECENT_ROOM_TTL_MS - 1, s)).not.toBeNull();
			expect(s.data.size).toBe(1);
			expect(loadRecentRoom(NOW + RECENT_ROOM_TTL_MS, s)).toBeNull();
			expect(s.data.size).toBe(0);
		});

		it('treats a time far in the future (a clock set back) as corrupt, but tolerates small skew', () => {
			const s = memory();
			rememberRecentRoom('KT7P', ID_A, NOW, s);
			expect(loadRecentRoom(NOW - 60_000, s)).not.toBeNull();
			expect(loadRecentRoom(NOW - 3_600_000, s)).toBeNull();
			expect(s.data.size).toBe(0);
		});
	});

	describe('corrupt storage is tolerated and cleaned', () => {
		it.each([
			['not JSON', '{nope'],
			['JSON null', 'null'],
			['a JSON number', '42'],
			['a JSON string', '"KT7P"'],
			['an array', '[]'],
			['a missing field', JSON.stringify({ code: 'KT7P', roomInstanceId: ID_A })],
			[
				'a code of the wrong shape',
				JSON.stringify({ code: 'kt7p', roomInstanceId: ID_A, lastJoinedAt: NOW })
			],
			[
				'an instance id of the wrong type',
				JSON.stringify({ code: 'KT7P', roomInstanceId: 7, lastJoinedAt: NOW })
			],
			[
				'a time that is a string',
				JSON.stringify({ code: 'KT7P', roomInstanceId: ID_A, lastJoinedAt: String(NOW) })
			]
		])('%s: no room, and the slot is removed', (_name, raw) => {
			const s = memory({ [RECENT_ROOM_KEY]: raw });
			expect(loadRecentRoom(NOW, s)).toBeNull();
			expect(s.data.has(RECENT_ROOM_KEY)).toBe(false);
		});

		it('drops fields nobody asked for (no nickname or anything else comes back)', () => {
			const s = memory({
				[RECENT_ROOM_KEY]: JSON.stringify({
					code: 'KT7P',
					roomInstanceId: ID_A,
					lastJoinedAt: NOW,
					nickname: 'Ana',
					answers: ['x']
				})
			});
			expect(loadRecentRoom(NOW, s)).toEqual({
				code: 'KT7P',
				roomInstanceId: ID_A,
				lastJoinedAt: NOW
			});
		});

		it('an empty storage has no room and nothing to clean', () => {
			const s = memory();
			expect(loadRecentRoom(NOW, s)).toBeNull();
			expect(s.data.size).toBe(0);
		});
	});

	describe('clearRecentRoom', () => {
		it('without a match it empties the slot', () => {
			const s = memory();
			rememberRecentRoom('KT7P', ID_A, NOW, s);
			clearRecentRoom(undefined, s);
			expect(s.data.size).toBe(0);
		});

		it('with a code it only clears that room (another tab may have joined a different game)', () => {
			const s = memory();
			rememberRecentRoom('BCDF', ID_B, NOW, s);
			clearRecentRoom({ code: 'KT7P' }, s);
			expect(s.data.size).toBe(1);
			clearRecentRoom({ code: 'bcdf' }, s);
			expect(s.data.size).toBe(0);
		});

		it('with an instance id it keeps the entry of a newer room that reuses the code', () => {
			const s = memory();
			rememberRecentRoom('KT7P', ID_B, NOW, s);
			clearRecentRoom({ code: 'KT7P', roomInstanceId: ID_A }, s);
			expect(s.data.size).toBe(1);
			clearRecentRoom({ code: 'KT7P', roomInstanceId: ID_B }, s);
			expect(s.data.size).toBe(0);
		});

		it('a session that never got a state (no instance id) clears the code', () => {
			const s = memory();
			rememberRecentRoom('KT7P', ID_A, NOW, s);
			clearRecentRoom({ code: 'KT7P', roomInstanceId: null }, s);
			expect(s.data.size).toBe(0);
		});

		it('removes a corrupt slot whatever the match', () => {
			const s = memory({ [RECENT_ROOM_KEY]: '{nope' });
			clearRecentRoom({ code: 'KT7P' }, s);
			expect(s.data.size).toBe(0);
		});

		it('an empty slot stays empty', () => {
			const s = memory();
			expect(() => clearRecentRoom({ code: 'KT7P' }, s)).not.toThrow();
			expect(s.data.size).toBe(0);
		});
	});

	describe('storage that is unavailable', () => {
		it('never throws when it is blocked', () => {
			expect(() => rememberRecentRoom('KT7P', ID_A, NOW, broken)).not.toThrow();
			expect(loadRecentRoom(NOW, broken)).toBeNull();
			expect(() => clearRecentRoom(undefined, broken)).not.toThrow();
			expect(() => clearRecentRoom({ code: 'KT7P' }, broken)).not.toThrow();
		});

		it('never throws when there is no storage at all', () => {
			expect(() => rememberRecentRoom('KT7P', ID_A, NOW, null)).not.toThrow();
			expect(loadRecentRoom(NOW, null)).toBeNull();
			expect(() => clearRecentRoom({ code: 'KT7P' }, null)).not.toThrow();
		});

		it('a full storage (setItem throws) leaves nothing to resume and no error', () => {
			const s = memory();
			s.setItem = () => {
				throw new DOMException('quota', 'QuotaExceededError');
			};
			expect(() => rememberRecentRoom('KT7P', ID_A, NOW, s)).not.toThrow();
			expect(loadRecentRoom(NOW, s)).toBeNull();
		});
	});
});
