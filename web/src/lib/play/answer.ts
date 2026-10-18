/**
 * Client mirror of `server/internal/game/normalize.go`: what the server calls
 * an empty answer / a wrong letter is decided with the SAME function, so the
 * UI never enables Basta for a call the server would reject.
 */

/** Maximum answer length in runes (`game.MaxAnswerLen`). */
export const MAX_ANSWER_LEN = 40;

const COMBINING_TILDE = '\u0303';

/** Letters without a canonical decomposition that the server folds to their plain spelling. */
const FOLDED: Record<string, string> = {
	ø: 'o',
	đ: 'd',
	ð: 'd',
	ł: 'l',
	ħ: 'h',
	ŧ: 't',
	ı: 'i',
	ß: 'ss',
	æ: 'ae',
	œ: 'oe'
};

const FORMAT_CHARS = /\p{Cf}/gu;
const IS_SPACE = /^\p{White_Space}$/u;
const IS_MARK = /^\p{Mn}$/u;

/** Removes the invisible Unicode format characters (zero width space, soft hyphen, BOM, bidi marks...). */
export function stripFormat(text: string): string {
	return text.replace(FORMAT_CHARS, '');
}

/**
 * The canonical form the server compares answers with: format characters
 * removed, trimmed, inner whitespace collapsed, lower-cased, NFD-decomposed and
 * stripped of every combining mark. In Spanish (`lang === 'es'`) `ñ` is kept as
 * its own letter; in every other language it folds to `n`.
 */
export function normalizeAnswer(text: string, lang: string): string {
	const out: string[] = [];
	let pendingSpace = false;
	for (const r of stripFormat(text).normalize('NFD')) {
		if (IS_SPACE.test(r)) {
			pendingSpace = out.length > 0;
			continue;
		}
		if (IS_MARK.test(r)) {
			if (
				r === COMBINING_TILDE &&
				lang === 'es' &&
				!pendingSpace &&
				out.length > 0 &&
				out[out.length - 1] === 'n'
			) {
				out[out.length - 1] = 'ñ';
			}
			continue;
		}
		if (pendingSpace) {
			out.push(' ');
			pendingSpace = false;
		}
		// Per code point, like Go's unicode.ToLower: String#toLowerCase on a whole
		// word applies context rules (final sigma) that the server does not.
		const lower = r.toLowerCase();
		out.push(FOLDED[lower] ?? lower);
	}
	return out.join('');
}

/** True when the normalized answer starts with the (normalized) letter; only the first character counts. */
export function startsWithLetter(text: string, letter: string, lang: string): boolean {
	const l = normalizeAnswer(letter, lang);
	return l !== '' && normalizeAnswer(text, lang).startsWith(l);
}

/** Status of one answer field as the player sees it. */
export type FieldStatus = 'empty' | 'wrong_letter' | 'valid';

export function fieldStatus(text: string, letter: string, lang: string): FieldStatus {
	if (normalizeAnswer(text, lang) === '') return 'empty';
	return startsWithLetter(text, letter, lang) ? 'valid' : 'wrong_letter';
}

/** The text sent to the server: trimmed and cut to the server limit (in runes). */
export function answerToSend(text: string): string {
	const trimmed = text.trim();
	const runes = Array.from(trimmed);
	return runes.length > MAX_ANSWER_LEN ? runes.slice(0, MAX_ANSWER_LEN).join('').trim() : trimmed;
}
