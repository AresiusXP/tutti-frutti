/**
 * Vote thresholds of the review. They mirror `server/internal/game/review.go`
 * exactly (the server is the authority; this is only for showing "1/2"):
 *
 *   - the electorate comes directly from RoundView.reviewVoters;
 *   - a reject vote needs `yes * 2 > eligible`, where `eligible` is the
 *     electorate without the author; only votes of electors count, and an
 *     author's vote on their own answer never does;
 *   - a merge proposal needs `yes * 2 > electorate`; the proposer is a yes.
 */
import type { ProposalView, RejectVote } from '$lib/protocol';

/** Smallest number of yes votes that is a strict majority of `voters`. */
export function majority(voters: number): number {
	return Math.floor(Math.max(voters, 0) / 2) + 1;
}

export interface RejectTally {
	/** Counting votes (from connected participants other than the author). */
	votes: number;
	/** Yes votes needed to reject the answer. */
	needed: number;
	/** Electors besides the author. */
	eligible: number;
	/** Counting voters, in the order of the electorate. */
	voters: string[];
	/** The viewer has a (raw) vote on this answer, counting or not. */
	mine: boolean;
	/** The votes currently reject the answer. */
	rejected: boolean;
}

export function rejectTally(
	electors: readonly string[],
	rejectVotes: readonly RejectVote[],
	category: number,
	target: string,
	viewer?: string
): RejectTally {
	const eligible = electors.length - (electors.includes(target) ? 1 : 0);
	const forTarget = rejectVotes.filter((v) => v.category === category && v.target === target);
	const counting = new Set(
		forTarget.filter((v) => v.voter !== target && electors.includes(v.voter)).map((v) => v.voter)
	);
	const voters = electors.filter((id) => counting.has(id));
	return {
		votes: voters.length,
		needed: majority(eligible),
		eligible,
		voters,
		mine: viewer !== undefined && forTarget.some((v) => v.voter === viewer),
		rejected: voters.length * 2 > eligible
	};
}

export interface MergeTally {
	/** Yes votes of connected participants, as counted by the server. */
	yes: number;
	/** Size of the electorate (connected participants). */
	total: number;
	/** Yes votes needed for a majority. */
	needed: number;
	/** The proposal currently has a majority (server verdict). */
	passed: boolean;
	/** The viewer's vote, or `null` when they have not voted. */
	mine: boolean | null;
}

export function mergeTally(
	proposal: ProposalView,
	reviewVoters: readonly string[],
	viewer?: string
): MergeTally {
	const total = reviewVoters.length;
	const mine = proposal.votes.find((v) => v.voter === viewer);
	return {
		yes: proposal.yes,
		total,
		needed: majority(total),
		passed: proposal.passed,
		mine: mine ? mine.yes : null
	};
}
