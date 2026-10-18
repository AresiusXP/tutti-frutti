/**
 * When does `next_round` lead to the final screen? Mirrors `nextRound` and
 * `beginRound` in `server/internal/game` (`review.go`, `round.go`): the game
 * finishes when `settings.rounds > 0 && roundNum >= settings.rounds`, or when
 * no letter is left to play. The alphabets and hard-letter lists mirror
 * `lang.go`.
 */
import type { Lang, Settings } from '$lib/protocol';

const ALPHABETS: Record<Lang, readonly string[]> = {
	es: 'A B C D E F G H I J K L M N Ñ O P Q R S T U V W X Y Z'.split(' '),
	en: 'A B C D E F G H I J K L M N O P Q R S T U V W X Y Z'.split(' ')
};

const HARD_LETTERS: Record<Lang, readonly string[]> = {
	es: ['K', 'Ñ', 'Q', 'W', 'X', 'Y', 'Z'],
	en: ['Q', 'U', 'V', 'X', 'Y', 'Z']
};

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
