import type { Lang } from '$lib/protocol';

/**
 * Alphabets and hard letters per lobby language. They MIRROR
 * `server/internal/game/lang.go` (a test parses that file and fails on drift);
 * the server stays authoritative and rejects settings whose round count does
 * not fit.
 */
export const LANGS: readonly Lang[] = ['es', 'en', 'nl'];

export const ALPHABETS: Record<Lang, readonly string[]> = {
	es: 'A B C D E F G H I J K L M N Ñ O P Q R S T U V W X Y Z'.split(' '),
	en: 'A B C D E F G H I J K L M N O P Q R S T U V W X Y Z'.split(' '),
	nl: 'A B C D E F G H I J K L M N O P Q R S T U V W X Y Z'.split(' ')
};

export const HARD_LETTERS: Record<Lang, readonly string[]> = {
	es: ['K', 'Ñ', 'Q', 'W', 'X', 'Y', 'Z'],
	en: ['Q', 'U', 'V', 'X', 'Y', 'Z'],
	nl: ['Q', 'X', 'Y']
};

/** The letters that can come up (alphabetical order) for a language and the hard-letter toggle. */
export function playableLetters(lang: Lang, excludeHard: boolean): string[] {
	const hard = excludeHard ? HARD_LETTERS[lang] : [];
	return ALPHABETS[lang].filter((letter) => !hard.includes(letter));
}

/** The most rounds that can be played: one per playable letter. */
export function maxRounds(lang: Lang, excludeHard: boolean): number {
	return playableLetters(lang, excludeHard).length;
}

/** 0 means unlimited; otherwise the value is forced into 1..max (non-integers are floored). */
export function clampRounds(rounds: number, lang: Lang, excludeHard: boolean): number {
	if (!Number.isFinite(rounds) || rounds <= 0) return 0;
	return Math.min(Math.max(Math.floor(rounds), 1), maxRounds(lang, excludeHard));
}
