import type { Lang, Listing } from '$lib/protocol';

/** Limits shared with the server (`server/internal/game/lang.go`; a test guards the values). */
export const MIN_CATEGORIES = 3;
export const MAX_CATEGORIES = 10;
export const MAX_CUSTOM_LEN = 24;

/** Letters that do not decompose (and the Dutch ligature ĳ, in every language); the server folds them the same way. */
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
	œ: 'oe',
	ĳ: 'ij'
};

const FORMAT_CHARS = /\p{Cf}/gu;
const SPACE = /[\s\u0085]/u;
const MARK = /\p{Mn}/u;

/** Characters (code points), the way the server counts the 24-char limit. */
export function runeCount(text: string): number {
	return Array.from(text).length;
}

/** Display text of a label: invisible format characters removed, trimmed, inner whitespace collapsed. */
export function cleanLabel(text: string): string {
	return text
		.replace(FORMAT_CHARS, '')
		.split(/[\s\u0085]+/u)
		.filter(Boolean)
		.join(' ');
}

/**
 * The comparison form of a label. Mirrors `Normalize` in
 * `server/internal/game/normalize.go`: format characters removed, whitespace
 * collapsed, lower-cased, diacritics stripped (NFD, no combining marks), the
 * ligature `ĳ` spelled `ij` (every language). In Spanish lobbies `ñ` stays a
 * letter of its own; elsewhere it folds to `n`.
 */
export function normalizeLabel(text: string, lang: Lang): string {
	let out = '';
	let pendingSpace = false;
	for (const char of text.replace(FORMAT_CHARS, '').normalize('NFD')) {
		if (SPACE.test(char)) {
			pendingSpace = out.length > 0;
			continue;
		}
		if (MARK.test(char)) {
			if (char === '\u0303' && lang === 'es' && !pendingSpace && out.endsWith('n')) {
				out = `${out.slice(0, -1)}ñ`;
			}
			continue;
		}
		if (pendingSpace) {
			out += ' ';
			pendingSpace = false;
		}
		// toLowerCase may add a combining mark (İ -> i + U+0307); the server's per-rune ToLower does not
		const lower = char.toLowerCase().replace(/\p{Mn}/gu, '');
		out += FOLDED[lower] ?? lower;
	}
	return out;
}

/** Accent- and case-insensitive form for the category search (ñ matches n). */
export function searchKey(text: string): string {
	return cleanLabel(text)
		.normalize('NFD')
		.replace(/\p{Mn}/gu, '')
		.toLowerCase()
		.replace(/[øđðłħŧıßæœĳ]/gu, (char) => FOLDED[char] ?? char);
}

/** Built-in labels of EVERY language, normalized for `lang`, mapped to the label shown in `lang`. */
export type BuiltinIndex = ReadonlyMap<string, string>;

/**
 * Builds the duplicate-detection index from the catalog listings that are
 * loaded. `listings[lang]` is required (it gives the label shown in the
 * message); the other languages are used when available.
 */
export function builtinIndex(listings: Partial<Record<Lang, Listing>>, lang: Lang): BuiltinIndex {
	const shown = new Map<string, string>();
	for (const category of listings[lang]?.categories ?? []) shown.set(category.id, category.label);
	const index = new Map<string, string>();
	for (const listing of Object.values(listings)) {
		for (const category of listing?.categories ?? []) {
			index.set(normalizeLabel(category.label, lang), shown.get(category.id) ?? category.label);
		}
	}
	return index;
}

export type CustomError = 'empty' | 'too_long' | 'duplicate';

export type CustomCheck =
	| { ok: true; label: string }
	| { ok: false; error: 'empty' }
	| { ok: false; error: 'too_long'; max: number }
	| { ok: false; error: 'duplicate'; existing: string };

/**
 * Validates a typed custom category the way the server will (empty, length,
 * duplicate of a built-in label in any language, duplicate of another
 * custom), so the host sees the problem before anything is sent.
 */
export function checkCustom(
	raw: string,
	lang: Lang,
	builtins: BuiltinIndex,
	customs: readonly string[]
): CustomCheck {
	const label = cleanLabel(raw);
	if (label === '') return { ok: false, error: 'empty' };
	if (runeCount(label) > MAX_CUSTOM_LEN) {
		return { ok: false, error: 'too_long', max: MAX_CUSTOM_LEN };
	}
	const key = normalizeLabel(label, lang);
	const builtin = builtins.get(key);
	if (builtin !== undefined) return { ok: false, error: 'duplicate', existing: builtin };
	const existing = customs.find((custom) => normalizeLabel(custom, lang) === key);
	if (existing !== undefined) return { ok: false, error: 'duplicate', existing };
	return { ok: true, label };
}
