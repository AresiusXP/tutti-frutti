export interface AvatarColor {
	id: 'banana' | 'orange' | 'strawberry' | 'lime' | 'grape' | 'blueberry';
	hex: string;
}

export const AVATAR_EMOJIS = ['🍌', '🍊', '🍓', '🍇', '🍉', '🍍'] as const;

export const AVATAR_COLORS: readonly AvatarColor[] = [
	{ id: 'banana', hex: '#ffd23f' },
	{ id: 'orange', hex: '#ff8a1f' },
	{ id: 'strawberry', hex: '#ff4f81' },
	{ id: 'lime', hex: '#7ddc3b' },
	{ id: 'grape', hex: '#9b6cff' },
	{ id: 'blueberry', hex: '#4cc9f0' }
];

export interface Avatar {
	emoji: string;
	color: AvatarColor['id'];
}

export const DEFAULT_AVATAR: Avatar = { emoji: AVATAR_EMOJIS[0], color: 'banana' };

/**
 * A random emoji + colour. A brand-new player gets one (instead of everybody
 * being the same yellow banana) so that players are told apart at a glance in
 * the lobby and the review. `rng` returns [0, 1) and is injectable for tests.
 */
export function randomAvatar(rng: () => number = Math.random): Avatar {
	const pick = <T>(items: readonly T[]): T =>
		items[Math.min(items.length - 1, Math.floor(rng() * items.length))];
	return { emoji: pick(AVATAR_EMOJIS), color: pick(AVATAR_COLORS).id };
}

export function isAvatar(value: unknown): value is Avatar {
	if (typeof value !== 'object' || value === null) return false;
	const v = value as Record<string, unknown>;
	return (
		typeof v.emoji === 'string' &&
		(AVATAR_EMOJIS as readonly string[]).includes(v.emoji) &&
		AVATAR_COLORS.some((c) => c.id === v.color)
	);
}

/** Hex of a palette id; any other string (the wire format allows any short colour) falls back to the first colour. */
export function colorHex(id: string): string {
	return (AVATAR_COLORS.find((c) => c.id === id) ?? AVATAR_COLORS[0]).hex;
}
