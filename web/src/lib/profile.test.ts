import { describe, expect, it } from 'vitest';
import { AVATAR_COLORS, AVATAR_EMOJIS, DEFAULT_AVATAR, randomAvatar } from './avatar';
import { AVATAR_KEY, NICKNAME_KEY, ProfileStore, type StorageLike } from './profile.svelte';

function memory(
	initial: Record<string, string> = {}
): StorageLike & { data: Record<string, string> } {
	const data = { ...initial };
	return {
		data,
		getItem: (k) => data[k] ?? null,
		setItem: (k, v) => {
			data[k] = v;
		}
	};
}

describe('randomAvatar', () => {
	it('always returns a palette emoji and colour, at both ends of rng', () => {
		for (const r of [0, 0.5, 0.999999, 1]) {
			const a = randomAvatar(() => r);
			expect(AVATAR_EMOJIS as readonly string[]).toContain(a.emoji);
			expect(AVATAR_COLORS.map((c) => c.id)).toContain(a.color);
		}
	});
	it('covers every emoji and colour', () => {
		const emojis = new Set<string>();
		const colors = new Set<string>();
		for (let i = 0; i < 60; i++) {
			const a = randomAvatar(() => (i % 6) / 6 + 0.01);
			emojis.add(a.emoji);
			colors.add(a.color);
		}
		expect(emojis.size).toBe(AVATAR_EMOJIS.length);
		expect(colors.size).toBe(AVATAR_COLORS.length);
	});
});

describe('ProfileStore avatar default', () => {
	it('keeps the fixed default unless randomDefault is on (deterministic tests)', () => {
		const storage = memory();
		expect(new ProfileStore(storage).avatar).toEqual(DEFAULT_AVATAR);
		expect(storage.data[AVATAR_KEY]).toBeUndefined();
	});

	it('gives a new player a random avatar and remembers it', () => {
		const storage = memory();
		const store = new ProfileStore(storage, { randomDefault: true, rng: () => 0.7 });
		expect(JSON.parse(storage.data[AVATAR_KEY])).toEqual(store.avatar);
		// the next visit reads the same avatar back, whatever the rng says
		const again = new ProfileStore(storage, { randomDefault: true, rng: () => 0 });
		expect(again.avatar).toEqual(store.avatar);
	});

	it('never overrides a saved avatar', () => {
		const saved = { emoji: '🍇', color: 'grape' };
		const storage = memory({ [AVATAR_KEY]: JSON.stringify(saved), [NICKNAME_KEY]: 'Ana' });
		const store = new ProfileStore(storage, { randomDefault: true, rng: () => 0 });
		expect(store.avatar).toEqual(saved);
		expect(store.nickname).toBe('Ana');
	});

	it('replaces a corrupted saved avatar and survives blocked storage', () => {
		const bad = memory({ [AVATAR_KEY]: '{not json' });
		expect(() => new ProfileStore(bad, { randomDefault: true })).not.toThrow();
		const blocked: StorageLike = {
			getItem: () => {
				throw new Error('blocked');
			},
			setItem: () => {
				throw new Error('blocked');
			}
		};
		const store = new ProfileStore(blocked, { randomDefault: true });
		expect(AVATAR_EMOJIS as readonly string[]).toContain(store.avatar.emoji);
	});

	it('works without storage at all', () => {
		const store = new ProfileStore(null, { randomDefault: true });
		expect(AVATAR_COLORS.map((c) => c.id)).toContain(store.avatar.color);
	});
});
