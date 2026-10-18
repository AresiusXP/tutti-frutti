/**
 * When does `next_round` lead to the final screen? Mirrors `nextRound` and
 * `beginRound` in `server/internal/game` (`review.go`, `round.go`): the game
 * finishes when `settings.rounds > 0 && roundNum >= settings.rounds`, or when
 * no letter is left to play. The alphabets and hard-letter lists mirror
 * `lang.go` (the tables live in `lobby/letters.ts`, which a test checks against it).
 */
import { ALPHABETS, HARD_LETTERS } from '$lib/lobby/letters';
import type { Lang, Settings } from '$lib/protocol';

/** Letters that can still be played: the alphabet without the used (and, if asked, the hard) ones. */
export function availableLetters(
	lang: Lang,
	excludeHard: boolean,
	used: readonly string[]
): string[] {
	const hard = excludeHard ? HARD_LETTERS[lang] : [];
	return (ALPHABETS[lang] ?? []).filter((l) => !used.includes(l) && !hard.includes(l));
}

export type LastRoundReason = 'rounds' | 'letters';

/** Why the round that just finished is the last one, or `null` when the game goes on. */
export function lastRoundReason(
	settings: Pick<Settings, 'rounds' | 'lang' | 'excludeHardLetters'>,
	roundNum: number,
	usedLetters: readonly string[]
): LastRoundReason | null {
	if (settings.rounds > 0 && roundNum >= settings.rounds) return 'rounds';
	if (availableLetters(settings.lang, settings.excludeHardLetters, usedLetters).length === 0) {
		return 'letters';
	}
	return null;
}
