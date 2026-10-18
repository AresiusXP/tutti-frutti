import type { Player } from '$lib/protocol';

export interface PlayerChanges {
	joined: string[];
	left: string[];
}

/** Names of the players that appeared / disappeared between two lists (for the polite live region). */
export function diffPlayers(
	before: readonly Pick<Player, 'id' | 'name'>[],
	after: readonly Pick<Player, 'id' | 'name'>[]
): PlayerChanges {
	const beforeIds = new Set(before.map((p) => p.id));
	const afterIds = new Set(after.map((p) => p.id));
	return {
		joined: after.filter((p) => !beforeIds.has(p.id)).map((p) => p.name),
		left: before.filter((p) => !afterIds.has(p.id)).map((p) => p.name)
	};
}
