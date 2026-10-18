import type { View } from '$lib/protocol';
import { parseDeadline } from './time';

/** How long a vote to stop stays open (`game.StopVoteDuration`); the server sends the real `endsAt`. */
export const STOP_VOTE_SECONDS = 15;

/**
 * What the viewer is in a vote to stop:
 * - `asker`: asked for it, waits for the others;
 * - `voter`: a connected participant who has not answered yet;
 * - `voted`: said yes, waits for the others;
 * - `onlooker`: spectators and anyone who is not in the electorate (offline when it opened).
 */
export type StopRole = 'asker' | 'voter' | 'voted' | 'onlooker';

export interface StopVoteInfo {
	/** Identifies one vote (who asked and when it ends): a new vote has a new key. */
	key: string;
	role: StopRole;
	callerId: string;
	/** `endsAt` in ms since the epoch (null when the server sent no usable time). */
	endsAt: number | null;
	yes: number;
	total: number;
}

/**
 * The open vote to stop of a snapshot, seen from the viewer; null when there is none.
 * It is state, not events, that drives the UI: the vote exists only in the `playing` phase.
 */
export function stopVoteInfo(
	view: View | null | undefined,
	participant: boolean
): StopVoteInfo | null {
	const vote = view?.phase === 'playing' ? view.round?.stopVote : undefined;
	if (!view || !vote) return null;
	const voters = vote.voters ?? [];
	const yes = (vote.yes ?? []).filter((id) => voters.includes(id));
	let role: StopRole = 'onlooker';
	if (vote.callerId === view.youId) role = 'asker';
	else if (participant && voters.includes(view.youId)) {
		role = yes.includes(view.youId) ? 'voted' : 'voter';
	}
	return {
		key: `${vote.callerId}|${vote.endsAt}`,
		role,
		callerId: vote.callerId,
		endsAt: parseDeadline(vote.endsAt),
		yes: yes.length,
		total: voters.length
	};
}
