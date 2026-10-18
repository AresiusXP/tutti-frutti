/**
 * A `FakeSession` for the playground that reacts to the review commands like
 * the server does (it recomputes the live result after each vote), so the
 * screens can be tried by hand. It is NOT used by the component tests, which
 * assert the exact commands sent.
 */
import { FakeSession } from '$lib/game/fake.svelte';
import { buildReviewView, endGame as finishGame, freeze, type ReviewState } from './fixtures';
import { pairBlocker } from './merge';

export class ReviewDemoSession extends FakeSession {
	#state: ReviewState;

	constructor(state: ReviewState) {
		super(buildReviewView(state));
		this.#state = state;
	}

	#sync() {
		this.setView(buildReviewView(this.#state));
	}

	#ctx(category: number) {
		const view = buildReviewView(this.#state);
		return {
			category,
			participants: view.round?.participants ?? [],
			result: view.round?.result,
			proposals: view.round?.proposals ?? []
		};
	}

	override voteReject(category: number, targetPlayer: string, withdraw?: boolean) {
		super.voteReject(category, targetPlayer, withdraw);
		const s = this.#state;
		if (targetPlayer === s.youId) return this.fail('self_vote', 'vote_reject');
		if ((s.answers[targetPlayer]?.[category] ?? '') === '') {
			return this.fail('empty_answer', 'vote_reject');
		}
		s.rejectVotes = s.rejectVotes.filter(
			(v) => !(v.category === category && v.target === targetPlayer && v.voter === s.youId)
		);
		if (!withdraw) s.rejectVotes.push({ category, target: targetPlayer, voter: s.youId });
		this.#sync();
	}

	override proposeMerge(category: number, playerA: string, playerB: string) {
		super.proposeMerge(category, playerA, playerB);
		const s = this.#state;
		const blocker = pairBlocker(this.#ctx(category), playerA, playerB);
		if (blocker === 'already_same') return this.fail('already_same', 'propose_merge');
		if (blocker === 'proposal_exists') return this.fail('proposal_exists', 'propose_merge');
		if (blocker) return this.fail('invalid_merge', 'propose_merge');
		s.proposals.push({
			id: s.nextProposalId++,
			category,
			playerA,
			playerB,
			proposedBy: s.youId,
			votes: [{ voter: s.youId, yes: true }]
		});
		this.#sync();
	}

	override voteMerge(proposalId: number, yes: boolean) {
		super.voteMerge(proposalId, yes);
		const s = this.#state;
		const proposal = s.proposals.find((p) => p.id === proposalId);
		if (!proposal) return this.fail('unknown_proposal', 'vote_merge');
		const mine = proposal.votes.find((v) => v.voter === s.youId);
		if (mine) mine.yes = yes;
		else proposal.votes.push({ voter: s.youId, yes });
		this.#sync();
	}

	override reviewNav(category: number) {
		super.reviewNav(category);
		this.#state.category = category;
		this.#sync();
	}

	override finishReview() {
		super.finishReview();
		freeze(this.#state);
		this.#sync();
	}

	override endGame() {
		super.endGame();
		finishGame(this.#state);
		this.#sync();
	}
}
