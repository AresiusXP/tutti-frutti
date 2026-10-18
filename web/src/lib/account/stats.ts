import type { Stats } from './types';

/** Share of games won, as a whole percentage (0-100); `null` before the first game (never divides by zero). */
export function winRate(stats: Stats): number | null {
	if (stats.gamesPlayed <= 0) return null;
	return Math.round((stats.wins / stats.gamesPlayed) * 100);
}

/** Average total points per game with one decimal; `null` before the first game. */
export function averagePoints(stats: Stats): number | null {
	if (stats.gamesPlayed <= 0) return null;
	return Math.round((stats.totalPoints / stats.gamesPlayed) * 10) / 10;
}
