import { describe, expect, it } from 'vitest';
import { AVATAR_KEY, NICKNAME_KEY, ProfileStore } from './profile.svelte';
import { NICKNAME_MAX, capNickname, normalizeNickname } from './nickname';

function memoryStorage(initial: Record<string, string> = {}) {
	const data = new Map(Object.entries(initial));
	return {
		getItem: (key: string) => data.get(key) ?? null,
		setItem: (key: string, value: string) => void data.set(key, value),
		data
	};
}

describe('normalizeNickname', () => {
	it('trims surrounding whitespace', () => {
		expect(normalizeNickname('  Ana  ')).toBe('Ana');
		expect(normalizeNickname('\tAna\n')).toBe('Ana');
	});

	it('collapses inner whitespace', () => {
		expect(normalizeNickname('Ana    María')).toBe('Ana María');
	});

	it('caps at 16 characters', () => {
		expect(NICKNAME_MAX).toBe(16);
		expect(normalizeNickname('x'.repeat(40))).toBe('x'.repeat(16));
		expect(normalizeNickname('x'.repeat(16))).toHaveLength(16);
	});

	it('trims again after capping so it never ends with a space', () => {
		expect(normalizeNickname('a'.repeat(15) + ' bbb')).toBe('a'.repeat(15));
	});

	it('counts characters, not UTF-16 units (emoji are not split)', () => {
		const emoji = '🍌'.repeat(20);
		expect(normalizeNickname(emoji)).toBe('🍌'.repeat(16));
		expect(capNickname(emoji)).toBe('🍌'.repeat(16));
	});

	it('returns an empty string for blank input', () => {
		expect(normalizeNickname('   ')).toBe('');
	});
});

describe('ProfileStore', () => {
	it('stores the trimmed, capped nickname and persists it', () => {
		const storage = memoryStorage();
		const profile = new ProfileStore(storage);
		expect(profile.setNickname('   ' + 'y'.repeat(30) + '   ')).toBe('y'.repeat(16));
		expect(profile.nickname).toBe('y'.repeat(16));
		expect(storage.data.get(NICKNAME_KEY)).toBe('y'.repeat(16));
	});

	it('remembers the nickname across instances (and re-normalises what it loads)', () => {
		const storage = memoryStorage({ [NICKNAME_KEY]: '  Mica  ' });
		expect(new ProfileStore(storage).nickname).toBe('Mica');
	});

	it('persists and restores the avatar, rejecting unknown values', () => {
		const storage = memoryStorage();
		const profile = new ProfileStore(storage);
		profile.setAvatar({ emoji: '🍓', color: 'strawberry' });
		expect(new ProfileStore(storage).avatar).toEqual({ emoji: '🍓', color: 'strawberry' });

		profile.setAvatar({ emoji: '💩' });
		expect(profile.avatar.emoji).toBe('🍓');

		const corrupted = memoryStorage({ [AVATAR_KEY]: '{nope' });
		expect(new ProfileStore(corrupted).avatar.color).toBe('banana');
	});

	it('works without storage', () => {
		const profile = new ProfileStore(null);
		expect(profile.setNickname(' Leo ')).toBe('Leo');
	});
});
