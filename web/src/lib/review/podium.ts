import type { View } from '$lib/protocol';
import { buildRanking, type RankRow } from './ranking';

export interface PodiumPlace {
	/** 1, 2 or 3 (competition ranking: a tie for first leaves no second place). */
	place: 1 | 2 | 3;
	medal: '🥇' | '🥈' | '🥉';
	rows: RankRow[];
}

export interface Podium {
	places: PodiumPlace[];
	/** Winners declared by the server (`view.winners`), or the first place when it is empty. */
	winnerIds: string[];
	/** The whole ranking (including players that left, last and unranked). */
	ranking: RankRow[];
}

const MEDALS = { 1: '🥇', 2: '🥈', 3: '🥉' } as const;

/**
 * Builds the podium of the final screen from `view.players`, `view.winners`
 * and the last round result. Places are the ranks 1, 2 and 3 by total score
 * (ties share a place). Players that left are not ranked, except winners the
 * server declared before they left (so the podium does not change under the
 * eyes of the table), who stay greyed.
 */
export function buildPodium(view: Pick<View, 'players' | 'winners' | 'round'>): Podium {
	const declared = view.winners ?? [];
	const ranking = buildRanking(view.players, view.round?.result, { keep: declared });
	const places = ([1, 2, 3] as const)
		.map((place) => ({
			place,
			medal: MEDALS[place],
			rows: ranking.filter((row) => row.rank === place)
		}))
		.filter((p) => p.rows.length > 0);
	const winnerIds =
		declared.length > 0 ? [...declared] : (places[0]?.rows ?? []).map((r) => r.player.id);
	return { places, winnerIds, ranking };
}
