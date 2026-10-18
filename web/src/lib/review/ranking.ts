import type { Player, RoundResult } from '$lib/protocol';

export interface RankRow {
	player: Player;
	/** Running total (`Player.totalScore`). */
	total: number;
	/** Points of the round shown by the result (0 when the player did not take part). */
	roundPoints: number;
	/** The player has a result for the round (they took part). */
	played: boolean;
	/** Competition rank (1, 1, 3, …) among the ranked players; `null` for players that left. */
	rank: number | null;
	/** Places gained since the previous round (negative = lost), `null` when not comparable. */
	change: number | null;
	/** The seat is flagged `left` (kept in the list, greyed). */
	left: boolean;
}

export interface RankingOptions {
	/** Compare with the standings before this round (needs at least one earlier round). */
	compare?: boolean;
	/** Players that left but still count as ranked (e.g. winners declared by the server). */
	keep?: readonly string[];
}

/** Competition ranking of totals sorted best first: ties share the rank, the next one skips (1, 1, 3). */
function competitionRanks(totals: readonly number[]): number[] {
	const ranks: number[] = [];
	totals.forEach((total, i) => ranks.push(i > 0 && totals[i - 1] === total ? ranks[i - 1] : i + 1));
	return ranks;
}

/** Ranks by running total (ties share a rank, then join order); players that left go last, unranked. */
export function buildRanking(
	players: readonly Player[],
	result: RoundResult | undefined,
	options: RankingOptions = {}
): RankRow[] {
	const keep = new Set(options.keep ?? []);
	const ranked = players.filter((p) => !p.left || keep.has(p.id));
	const gone = players.filter((p) => p.left && !keep.has(p.id));
	const roundPoints = (p: Player) => result?.players[p.id]?.total ?? 0;
	const byJoin = (a: Player, b: Player) => a.joinedSeq - b.joinedSeq;
	const byTotal = (a: Player, b: Player) => b.totalScore - a.totalScore || byJoin(a, b);

	const sorted = [...ranked].sort(byTotal);
	const ranks = competitionRanks(sorted.map((p) => p.totalScore));

	const before = new Map<string, number>();
	if (options.compare) {
		const prior = (p: Player) => p.totalScore - roundPoints(p);
		const order = [...ranked].sort((a, b) => prior(b) - prior(a) || byJoin(a, b));
		const priorRanks = competitionRanks(order.map(prior));
		order.forEach((p, i) => before.set(p.id, priorRanks[i]));
	}

	const row = (p: Player, rank: number | null): RankRow => {
		const prev = before.get(p.id);
		return {
			player: p,
			total: p.totalScore,
			roundPoints: roundPoints(p),
			played: !!result?.players[p.id],
			rank,
			change: rank !== null && prev !== undefined ? prev - rank : null,
			left: p.left
		};
	};
	return [
		...sorted.map((p, i) => row(p, ranks[i])),
		...[...gone].sort(byTotal).map((p) => row(p, null))
	];
}

export interface HistoryEntry {
	player: Player;
	total: number;
}

export interface HistoryRound {
	round: number;
	letter: string;
	/** Players of the round, best first (ties in join order). */
	entries: HistoryEntry[];
	/** Highest total of the round. */
	best: number;
}

/** The per-round history for the final screen. Players that are not in `players` any more are skipped. */
export function buildHistory(
	history: readonly RoundResult[],
	players: readonly Player[]
): HistoryRound[] {
	return history.map((r) => {
		const entries = players
			.filter((p) => r.players[p.id])
			.map((player) => ({ player, total: r.players[player.id].total }))
			.sort((a, b) => b.total - a.total || a.player.joinedSeq - b.player.joinedSeq);
		return { round: r.round, letter: r.letter, entries, best: entries[0]?.total ?? 0 };
	});
}
