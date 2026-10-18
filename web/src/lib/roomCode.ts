/** Room codes: 4 chars, no vowels (no words) and no look-alikes (0/1/O/I). */
export const ROOM_CODE_ALPHABET = 'BCDFGHJKLMNPQRSTVWXZ23456789';
export const ROOM_CODE_LENGTH = 4;

const ALLOWED = new Set(ROOM_CODE_ALPHABET);

/**
 * Upper-cases ASCII letters only. `String.prototype.toUpperCase` is Unicode-aware
 * and would expand some characters into valid code characters ("ß" -> "SS",
 * "ſ" -> "S", "ﬁ" -> "FI"), letting non-codes through.
 */
function asciiUpper(input: string): string {
	return input.replace(/[a-z]/g, (ch) => String.fromCharCode(ch.charCodeAt(0) - 32));
}

/** True for a single character that is valid in a room code (case-insensitive, ASCII only). */
export function isRoomCodeChar(ch: string): boolean {
	return ch.length === 1 && ALLOWED.has(asciiUpper(ch));
}

/**
 * Lenient filter for typing/autofill: upper-cases (ASCII only), drops every
 * character outside the alphabet and caps the length. Do not use it to decide
 * whether pasted text "is" a code, see `normalizePastedCode`.
 */
export function sanitizeRoomCode(input: string): string {
	let out = '';
	for (const ch of asciiUpper(input)) {
		if (ALLOWED.has(ch)) out += ch;
		if (out.length === ROOM_CODE_LENGTH) break;
	}
	return out;
}

const CODE_PATTERN = new RegExp(`^[${ROOM_CODE_ALPHABET}]{${ROOM_CODE_LENGTH}}$`);

/** The code in `text` (ASCII-upper-cased) when it is exactly one valid code, else ''. */
function exactCode(text: string): string {
	const upper = asciiUpper(text);
	return CODE_PATTERN.test(upper) ? upper : '';
}

/**
 * Strict normalisation of pasted text. Returns a code only when the text is
 *  - a bare code, ignoring surrounding whitespace and `-`/whitespace separators
 *    inside ("kt7p", " KT 7P ", "K-T7P"), or
 *  - an http(s) invite link whose path ends in a valid code
 *    ("https://host/KT7P", "https://host/KT7P/?utm=1#x").
 *
 * Anything else (prose such as "not a code", more than 4 valid characters, a
 * link with another path or an invalid code) yields '' and callers must leave
 * the current input untouched, instead of scavenging letters out of the text.
 */
export function normalizePastedCode(text: string): string {
	const trimmed = text.trim();
	if (/^[a-z][a-z0-9+.-]*:/i.test(trimmed)) {
		let url: URL;
		try {
			url = new URL(trimmed);
		} catch {
			return '';
		}
		if (url.protocol !== 'http:' && url.protocol !== 'https:') return '';
		const last = url.pathname.split('/').filter(Boolean).at(-1) ?? '';
		return exactCode(last);
	}
	return exactCode(trimmed.replace(/[\s-]+/gu, ''));
}

export function isCompleteRoomCode(code: string): boolean {
	return code.length === ROOM_CODE_LENGTH && sanitizeRoomCode(code) === code;
}

export interface CodeEdit {
	/** New code value (always a gap-free prefix of valid characters). */
	value: string;
	/** Index of the box that should receive focus next. */
	focus: number;
}

/**
 * Applies text typed (or autofilled) into box `index`. A single new character
 * overwrites the box; longer input (autofill, IME bursts) is spread over the
 * following boxes. Invalid characters are ignored.
 */
export function typeIntoBox(value: string, index: number, raw: string): CodeEdit {
	const at = Math.min(Math.max(index, 0), value.length, ROOM_CODE_LENGTH - 1);
	let incoming = sanitizeRoomCode(raw);
	// Typing over a filled box yields "<old><new>" or "<new><old>": keep the new one.
	const old = value[at];
	if (old && incoming.length === 2) {
		incoming = incoming[0] === old ? incoming[1] : incoming[0];
	}
	if (!incoming) return { value, focus: Math.min(index, ROOM_CODE_LENGTH - 1) };
	const next = sanitizeRoomCode(value.slice(0, at) + incoming + value.slice(at + incoming.length));
	return { value: next, focus: Math.min(at + incoming.length, ROOM_CODE_LENGTH - 1) };
}

/** Backspace in box `index`: clears it, or the previous one when it is already empty. */
export function backspaceAt(value: string, index: number): CodeEdit {
	if (index < value.length) {
		return { value: value.slice(0, index) + value.slice(index + 1), focus: index };
	}
	const prev = Math.min(index, value.length) - 1;
	if (prev < 0) return { value, focus: 0 };
	return { value: value.slice(0, prev) + value.slice(prev + 1), focus: prev };
}
