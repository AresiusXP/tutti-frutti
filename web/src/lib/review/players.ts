import type { Player } from '$lib/protocol';
import type { Avatar } from '$lib/avatar';

/** The protocol avatar (`color` is a free string) as the `AvatarBadge` type. */
export function avatarOf(player: Pick<Player, 'avatar'>): Avatar {
	return player.avatar as Avatar;
}

/** "Ana, Beto y Caro" / "Ana, Beto, and Caro" in the given UI locale. */
export function joinNames(names: readonly string[], locale: string): string {
	try {
		return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(names);
	} catch {
		return names.join(', ');
	}
}

/** Player lookup that survives unknown ids (shows the id instead of crashing). */
export function playerName(players: readonly Player[], id: string): string {
	return players.find((p) => p.id === id)?.name ?? id;
}
