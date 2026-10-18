/**
 * The game rules as DESIGN.md §3 states them. They are written out here on purpose (and not
 * imported from the web app): if the app drifts from the design, a test must notice.
 */
export type Lang = 'es' | 'en' | 'nl';

export const ALPHABET: Record<Lang, string[]> = {
	es: 'A B C D E F G H I J K L M N Ñ O P Q R S T U V W X Y Z'.split(' '),
	en: 'A B C D E F G H I J K L M N O P Q R S T U V W X Y Z'.split(' '),
	nl: 'A B C D E F G H I J K L M N O P Q R S T U V W X Y Z'.split(' ')
};

export const HARD_LETTERS: Record<Lang, string[]> = {
	es: ['K', 'Ñ', 'Q', 'W', 'X', 'Y', 'Z'],
	en: ['Q', 'U', 'V', 'X', 'Y', 'Z'],
	nl: ['Q', 'X', 'Y']
};

/** The silent alphabet shows each letter for this long: letter = letters[floor(elapsed / 120 ms) mod n]. */
export const LETTER_TICK_MS = 120;

export function playableLetters(lang: Lang, excludeHard: boolean): string[] {
	return ALPHABET[lang].filter((l) => !excludeHard || !HARD_LETTERS[lang].includes(l));
}

/** The default categories ("Clásicas") in game order, per lobby language (catalog order of the defaults). */
export const CLASSIC_CATEGORIES: Record<Lang, string[]> = {
	es: ['Nombre', 'País', 'Animal', 'Comida', 'Color', 'Marca'],
	en: ['Name', 'Country', 'Animal', 'Food', 'Color', 'Brand'],
	nl: ['Naam', 'Land', 'Dier', 'Eten', 'Kleur', 'Merk']
};

export const MAX_PLAYERS = 10;
export const UNIQUE_POINTS = 10;
export const SHARED_POINTS = 5;

/**
 * The exact words of the Basta call: the main button of the playing screen and the full-screen overlay.
 * English and Dutch say just "Stop!", with no Spanish word or character.
 */
export const BASTA_BUTTON: Record<Lang, string> = { es: '¡BASTA!', en: 'Stop!', nl: 'Stop!' };
export const BASTA_OVERLAY: Record<Lang, string> = {
	es: '¡Basta para mí, basta para todos!',
	en: 'Stop!',
	nl: 'Stop!'
};

/** How long the others have to agree to a vote to stop (DESIGN.md "Vote to stop"). */
export const STOP_VOTE_SECONDS = 15;
