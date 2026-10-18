import type { Avatar as WireAvatar, View } from '$lib/protocol';
import type { Avatar } from '$lib/avatar';

export interface ProgressEntry {
	id: string;
	name: string;
	avatar: Avatar;
	you: boolean;
	offline: boolean;
	filled: number;
	total: number;
	done: boolean;
	/** 0-100, for the ring around the avatar. */
	percent: number;
}

/** The wire avatar carries the colour id the picker produced; unknown values fall back in `colorHex`. */
export function badgeAvatar(avatar: WireAvatar): Avatar {
	return { emoji: avatar.emoji, color: avatar.color as Avatar['color'] };
}

/**
 * Progress of every participant who is still in the game, in join order.
 * Others come from `round.filled` (the view never reveals their answers); the
 * viewer's own entry uses `ownFilled`, counted from the local drafts, so it
 * reacts while typing and not only after the server echoes it.
 */
export function playerProgress(view: View, ownFilled: number): ProgressEntry[] {
	const round = view.round;
	if (!round) return [];
	const total = view.settings.categories.length;
	const entries: ProgressEntry[] = [];
	for (const id of round.participants) {
		const p = view.players.find((candidate) => candidate.id === id);
		if (!p || p.left) continue;
		const you = id === view.youId;
		const filled = Math.min(total, Math.max(0, you ? ownFilled : (round.filled[id] ?? 0)));
		entries.push({
			id,
			name: p.name,
			avatar: badgeAvatar(p.avatar),
			you,
			offline: !p.connected,
			filled,
			total,
			done: total > 0 && filled >= total,
			percent: total > 0 ? Math.round((filled / total) * 100) : 0
		});
	}
	return entries;
}
