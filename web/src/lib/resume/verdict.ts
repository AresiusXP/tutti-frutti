import { ApiError, type RoomInfo } from '$lib/api';
import type { RecentRoom } from './recent';

/**
 * What Home does with the remembered room after asking the server about its code (`GET /api/rooms/{code}`).
 *
 *  - `forget`: the room is gone (404), the code now belongs to another room (different `roomInstanceId`) or the
 *    server refuses the guest for safety reasons (`join_unavailable`).
 *  - `offer`: show the card. Full, finished and in-progress rooms are offered too (`joinable: false` is about NEW
 *    seats): the room route joins and the server decides, an existing player gets the seat back by the guest cookie.
 *    So are a failed check (offline, busy, rate limited, timed out) and a server that does not report an instance
 *    id yet (older release): the join itself stays authoritative.
 */
export type ResumeVerdict = 'offer' | 'forget';

export function verdictOf(
	recent: RecentRoom,
	result: { info: RoomInfo } | { error: unknown }
): ResumeVerdict {
	if ('error' in result) {
		const e = result.error;
		if (
			e instanceof ApiError &&
			(e.status === 404 || e.code === 'room_not_found' || e.code === 'join_unavailable')
		) {
			return 'forget';
		}
		return 'offer';
	}
	const seen = result.info.roomInstanceId;
	return typeof seen === 'string' && seen !== '' && seen !== recent.roomInstanceId
		? 'forget'
		: 'offer';
}
