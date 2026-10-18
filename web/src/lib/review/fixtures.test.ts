import { describe, expect, it } from 'vitest';
import { ReviewDemoSession } from './demo';
import {
	BASE_TOTALS,
	buildReviewView,
	reviewState,
	reviewView,
	scoreState,
	vote,
	type ReviewOptions
} from './fixtures';

const cells = (options: ReviewOptions = {}) => {
	const result = reviewView(options).round!.result!;
	return (id: string) => result.players[id].cells;
};

describe('review fixtures', () => {
	it('is a 4-player, 6-category round with letter F', () => {
		const view = reviewView();
		expect(view.phase).toBe('review');
		expect(view.round?.letter).toBe('F');
		expect(view.round?.participants).toEqual(['p1', 'p2', 'p3', 'p4']);
		expect(view.settings.categories).toHaveLength(6);
		expect(view.categoryLabels).toEqual(['Nombre', 'País', 'Animal', 'Comida', 'Color', 'Marca']);
	});

	it('scores unique, shared, empty and wrong-letter cells like the server', () => {
		const of = cells();
		expect(of('p1')[0]).toMatchObject({ status: 'valid', points: 5, shared: true });
		expect(of('p3')[0]).toMatchObject({ status: 'valid', points: 10, shared: false });
		expect(of('p4')[0]).toMatchObject({ status: 'wrong_letter', points: 0 });
		expect(of('p3')[2]).toMatchObject({ status: 'empty', points: 0 });
		expect(of('p1')[4]).toMatchObject({ points: 5, shared: true });
	});

	it('rejects an answer when the votes pass the threshold', () => {
		const votes = [vote('p2', 'p3', 3), vote('p4', 'p3', 3)];
		expect(cells({ rejectVotes: votes.slice(0, 1) })('p3')[3].status).toBe('valid');
		expect(cells({ rejectVotes: votes })('p3')[3]).toMatchObject({ status: 'rejected', points: 0 });
	});

	it('merges answers when a proposal passes', () => {
		const proposal = (voters: string[]) => ({
			category: 3,
			playerA: 'p1',
			playerB: 'p2',
			proposedBy: 'p1',
			votes: voters.map((voter) => ({ voter, yes: true }))
		});
		expect(cells({ proposals: [proposal(['p1', 'p2'])] })('p1')[3].points).toBe(10);
		expect(cells({ proposals: [proposal(['p1', 'p2', 'p3'])] })('p1')[3]).toMatchObject({
			points: 5,
			shared: true
		});
		const view = reviewView({ proposals: [proposal(['p1', 'p2'])] });
		expect(view.round?.proposals[0]).toMatchObject({ id: 1, yes: 2, passed: false });
	});

	it('disconnected voters do not count in the proposal tally', () => {
		const view = reviewView({
			offline: ['p3'],
			proposals: [
				{
					category: 3,
					playerA: 'p1',
					playerB: 'p2',
					proposedBy: 'p1',
					votes: [
						{ voter: 'p1', yes: true },
						{ voter: 'p3', yes: true }
					]
				}
			]
		});
		expect(view.round?.proposals[0]).toMatchObject({ yes: 1, passed: false });
	});

	it('has a mid-game joiner who watches', () => {
		const view = reviewView({ spectator: true });
		expect(view.youId).toBe('p5');
		expect(view.round?.participants).not.toContain('p5');
		expect(view.players).toHaveLength(5);
	});

	it('keeps the running totals out of the review and adds the round in the summary', () => {
		const review = reviewView();
		expect(review.players.map((p) => p.totalScore)).toEqual(Object.values(BASE_TOTALS));
		const summary = reviewView({ phase: 'round_summary' });
		const round = summary.round!.result!;
		for (const p of summary.players) {
			expect(p.totalScore).toBe(BASE_TOTALS[p.id] + round.players[p.id].total);
		}
		expect(summary.history).toHaveLength(3);
		expect(summary.history.at(-1)).toEqual(round);
		expect(summary.round?.rejectVotes).toEqual([]);
	});

	it('builds a 3-round history whose earlier rounds add up to the base totals', () => {
		const view = reviewView({ phase: 'final' });
		const earlier = view.history.slice(0, 2);
		for (const id of ['p1', 'p2', 'p3', 'p4']) {
			expect(earlier.reduce((sum, r) => sum + r.players[id].total, 0)).toBe(BASE_TOTALS[id]);
		}
		expect(view.history.map((r) => r.round)).toEqual([1, 2, 3]);
	});

	it('crowns the best total, ties included, and honours explicit totals', () => {
		const tie = reviewView({
			phase: 'final',
			finalTotals: { p1: 105, p2: 105, p3: 80, p4: 75 }
		});
		expect(tie.winners).toEqual(['p1', 'p2']);
		expect(tie.players.map((p) => p.totalScore)).toEqual([105, 105, 80, 75]);
		expect(reviewView({ phase: 'final' }).winners).toEqual(['p2']);
	});

	it('lets a declared winner be someone who left', () => {
		const view = reviewView({ phase: 'final', left: ['p2'], winners: ['p2'] });
		expect(view.winners).toEqual(['p2']);
		expect(view.players.find((p) => p.id === 'p2')?.left).toBe(true);
	});

	it('speaks English when the lobby does', () => {
		const view = reviewView({ lang: 'en' });
		expect(view.categoryLabels[1]).toBe('Country');
		expect(view.round?.answers.p1[1]).toBe('France');
		expect(view.settings.lang).toBe('en');
	});

	it('scoreState is the live result of the state', () => {
		const state = reviewState();
		expect(scoreState(state)).toEqual(buildReviewView(state).round?.result);
	});
});

describe('ReviewDemoSession', () => {
	it('recomputes the view after each command, like the server', () => {
		const session = new ReviewDemoSession(reviewState({ category: 3 }));
		session.voteReject(3, 'p3');
		expect(session.sent).toEqual([{ cmd: 'voteReject', args: [3, 'p3'] }]);
		expect(session.view?.round?.rejectVotes).toEqual([{ category: 3, target: 'p3', voter: 'p1' }]);

		session.voteReject(3, 'p3', true);
		expect(session.view?.round?.rejectVotes).toEqual([]);

		session.proposeMerge(3, 'p1', 'p2');
		expect(session.view?.round?.proposals).toHaveLength(1);
		session.proposeMerge(3, 'p1', 'p2');
		expect(session.lastError?.code).toBe('proposal_exists');
		session.voteMerge(1, false);
		expect(session.view?.round?.proposals[0].votes).toEqual([{ voter: 'p1', yes: false }]);
		session.voteMerge(99, true);
		expect(session.lastError?.code).toBe('unknown_proposal');

		session.reviewNav(5);
		expect(session.view?.round?.reviewCategory).toBe(5);
	});

	it('refuses what the server refuses', () => {
		const session = new ReviewDemoSession(reviewState({ category: 2 }));
		session.voteReject(2, 'p1');
		expect(session.lastError?.code).toBe('self_vote');
		session.voteReject(2, 'p3');
		expect(session.lastError?.code).toBe('empty_answer');
		session.proposeMerge(2, 'p1', 'p1');
		expect(session.lastError?.code).toBe('invalid_merge');
		session.proposeMerge(2, 'p1', 'p4');
		expect(session.lastError?.code).toBe('already_same');
		expect(session.view?.round?.proposals).toEqual([]);
	});

	it('walks review -> summary -> final', () => {
		const session = new ReviewDemoSession(reviewState());
		session.finishReview();
		expect(session.view?.phase).toBe('round_summary');
		expect(session.view?.history).toHaveLength(3);
		session.endGame();
		expect(session.view?.phase).toBe('final');
		expect(session.view?.winners).toEqual(['p2']);
	});
});
