import { ROOM_CODE_ALPHABET, ROOM_CODE_LENGTH } from '$lib/roomCode';

/** `/` or `/<ROOMCODE>`: the only places the OAuth callback may send the user back to. */
export type ReturnTo = '/' | `/${string}`;

const ROOM_PATH = new RegExp(`^/[${ROOM_CODE_ALPHABET}]{${ROOM_CODE_LENGTH}}$`);

/**
 * Same allow-list as the server (`returnTo` is exactly `/` or `/<ROOMCODE>`,
 * case-insensitive, normalised to upper case); anything else becomes `/`.
 * ASCII-only upper-casing so "ß"-style expansions never produce a valid code.
 */
export function safeReturnTo(raw: unknown): ReturnTo {
	if (typeof raw !== 'string') return '/';
	const upper = raw.replace(/[a-z]/g, (ch) => String.fromCharCode(ch.charCodeAt(0) - 32));
	return ROOM_PATH.test(upper) ? (upper as ReturnTo) : '/';
}

/** The login endpoint for a (sanitised) return path. */
export function loginPath(returnTo: unknown): string {
	return `/api/auth/google/login?returnTo=${encodeURIComponent(safeReturnTo(returnTo))}`;
}
