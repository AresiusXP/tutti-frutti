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

export function isAvatar(value: unknown): value is Avatar {
	if (typeof value !== 'object' || value === null) return false;
	const v = value as Record<string, unknown>;
	return (
		typeof v.emoji === 'string' &&
		(AVATAR_EMOJIS as readonly string[]).includes(v.emoji) &&
		AVATAR_COLORS.some((c) => c.id === v.color)
	);
}

export function colorHex(id: AvatarColor['id']): string {
	return (AVATAR_COLORS.find((c) => c.id === id) ?? AVATAR_COLORS[0]).hex;
}
