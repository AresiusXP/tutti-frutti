import { AVATAR_COLORS, DEFAULT_AVATAR, type Avatar as UiAvatar } from '$lib/avatar';
import type { Avatar as WireAvatar } from '$lib/protocol';

/**
 * The server stores any short colour string; the UI knows the six palette ids.
 * An unknown colour falls back to the default one so a badge always renders.
 */
export function uiAvatar(avatar: WireAvatar): UiAvatar {
	const color = AVATAR_COLORS.find((c) => c.id === avatar.color)?.id ?? DEFAULT_AVATAR.color;
	return { emoji: avatar.emoji, color };
}
