import { describe, expect, it } from 'vitest';
import { clearRoomMark, hasRoomMark, markRoom, roomMarkKey } from './roomMark';

function memory() {
	const data = new Map<string, string>();
	return {
		data,
		getItem: (k: string) => data.get(k) ?? null,
		setItem: (k: string, v: string) => void data.set(k, v),
		removeItem: (k: string) => void data.delete(k)
	};
}

describe('room mark (sessionStorage tf.room.<CODE>)', () => {
	it('marks, reads and clears a room, case-insensitively', () => {
		const s = memory();
		expect(roomMarkKey('kt7p')).toBe('tf.room.KT7P');
		expect(hasRoomMark('KT7P', s)).toBe(false);
		markRoom('kt7p', s);
		expect(s.data.get('tf.room.KT7P')).toBe('1');
		expect(hasRoomMark('KT7P', s)).toBe(true);
		expect(hasRoomMark('ABCD', s)).toBe(false);
		clearRoomMark('KT7P', s);
		expect(hasRoomMark('KT7P', s)).toBe(false);
	});

	it('never throws when storage is blocked or missing', () => {
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
		expect(() => markRoom('KT7P', broken)).not.toThrow();
		expect(hasRoomMark('KT7P', broken)).toBe(false);
		expect(() => clearRoomMark('KT7P', broken)).not.toThrow();
		expect(() => markRoom('KT7P', null)).not.toThrow();
		expect(hasRoomMark('KT7P', null)).toBe(false);
		expect(() => clearRoomMark('KT7P', null)).not.toThrow();
	});

	it('defaults to the global sessionStorage when there is one', () => {
		const s = memory();
		const original = (globalThis as { sessionStorage?: unknown }).sessionStorage;
		(globalThis as { sessionStorage?: unknown }).sessionStorage = s;
		try {
			markRoom('KT7P');
			expect(hasRoomMark('KT7P')).toBe(true);
			clearRoomMark('KT7P');
			expect(s.data.size).toBe(0);
		} finally {
			(globalThis as { sessionStorage?: unknown }).sessionStorage = original;
		}
	});
});
