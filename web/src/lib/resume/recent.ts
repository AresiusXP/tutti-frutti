/**
 * "Resume recent game": the ONE game this browser joined last, in localStorage under `tf.recentRoom`, so
 * that a browser process killed by Android (which loses the per-tab sessionStorage marker `tf.room.<CODE>`)
 * does not strand a player who is still seated. Only `{code, roomInstanceId, lastJoinedAt}` is kept: no
 * nickname, no avatar, no answers (answers the server received survive a reconnection by themselves; text
 * typed but not yet sent can be lost when the process dies).
 *
 * The slot is written when a join succeeds (the first `state` of a connection, see `RoomRoute`), replaced by the
 * next join, forgotten after 24 hours (checked on every read) and cleared when the player leaves, is kicked, the
 * room is closed, the join is refused or Home finds the code reused by another room. `roomInstanceId` only tells
 * a room from a recycled code; it is not a credential (the join still needs the code and the guest cookie).
 * Every function tolerates corrupt data and unavailable storage and never throws.
 */
import { isCompleteRoomCode } from '$lib/roomCode';

export const RECENT_ROOM_KEY = 'tf.recentRoom';
export const RECENT_ROOM_TTL_MS = 24 * 60 * 60 * 1000;
/** A stamp this far ahead of the clock is not believable (clock set back): treated as corrupt. */
const FUTURE_SKEW_MS = 5 * 60 * 1000;
const INSTANCE_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface RecentRoom {
	/** Canonical (upper-case) room code. */
	code: string;
	/** `State.roomInstanceId`: identifies the room, not its reusable code. */
	roomInstanceId: string;
	/** `Date.now()` of the last successful join, in ms. */
	lastJoinedAt: number;
}

export type RecentStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function browserStorage(): RecentStorage | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

const canonical = (code: string) => code.trim().toUpperCase();

/** Writes the one slot after a successful join. Anything that is not a plausible code / instance id is ignored. */
export function rememberRecentRoom(
	code: string,
	roomInstanceId: string,
	now: number = Date.now(),
	storage: RecentStorage | null = browserStorage()
): void {
	const room = canonical(code);
	if (!isCompleteRoomCode(room) || !INSTANCE_ID.test(roomInstanceId) || !Number.isFinite(now))
		return;
	const value: RecentRoom = { code: room, roomInstanceId, lastJoinedAt: Math.trunc(now) };
	try {
		storage?.setItem(RECENT_ROOM_KEY, JSON.stringify(value));
	} catch {
		// full or blocked storage: there is just nothing to resume later
	}
}

/** The three known fields of a stored slot, rebuilt (anything else somebody wrote there is dropped); `null` when malformed. */
function parse(raw: string): RecentRoom | null {
	let data: unknown;
	try {
		data = JSON.parse(raw);
	} catch {
		return null;
	}
	if (typeof data !== 'object' || data === null) return null;
	const { code, roomInstanceId, lastJoinedAt } = data as Record<string, unknown>;
	if (typeof code !== 'string' || !isCompleteRoomCode(code)) return null;
	if (typeof roomInstanceId !== 'string' || !INSTANCE_ID.test(roomInstanceId)) return null;
	if (typeof lastJoinedAt !== 'number' || !Number.isFinite(lastJoinedAt)) return null;
	return { code, roomInstanceId, lastJoinedAt };
}

/** The remembered room, or `null` when there is none, it is older than 24 h or the slot is corrupt (those are removed). */
export function loadRecentRoom(
	now: number = Date.now(),
	storage: RecentStorage | null = browserStorage()
): RecentRoom | null {
	try {
		const raw = storage?.getItem(RECENT_ROOM_KEY) ?? null;
		if (raw === null) return null;
		const room = parse(raw);
		const age = room === null ? 0 : now - room.lastJoinedAt;
		if (room !== null && age < RECENT_ROOM_TTL_MS && age >= -FUTURE_SKEW_MS) return room;
		storage?.removeItem(RECENT_ROOM_KEY);
		return null;
	} catch {
		return null;
	}
}

/**
 * Forgets the slot. With `match` only when it holds that room: another tab may have joined a different game
 * meanwhile, and leaving this one must not erase it. A `roomInstanceId` in `match` also has to agree, so the
 * ending of an old room never erases the entry of a newer room that reuses the code. A corrupt slot is removed.
 */
export function clearRecentRoom(
	match?: { code: string; roomInstanceId?: string | null },
	storage: RecentStorage | null = browserStorage()
): void {
	try {
		if (match) {
			const raw = storage?.getItem(RECENT_ROOM_KEY) ?? null;
			if (raw === null) return;
			const held = parse(raw);
			if (held !== null) {
				if (held.code !== canonical(match.code)) return;
				if (match.roomInstanceId && held.roomInstanceId !== match.roomInstanceId) return;
			}
		}
		storage?.removeItem(RECENT_ROOM_KEY);
	} catch {
		// nothing to clean
	}
}
