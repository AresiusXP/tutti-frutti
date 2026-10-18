export const NICKNAME_MAX = 16;

/** Caps at 16 characters (code points, so emoji are not split) without touching whitespace. */
export function capNickname(raw: string): string {
	return Array.from(raw).slice(0, NICKNAME_MAX).join('');
}

/** The canonical nickname: inner whitespace collapsed, trimmed, at most 16 characters. */
export function normalizeNickname(raw: string): string {
	return capNickname(raw.replace(/\s+/gu, ' ').trim()).trim();
}
