import { describe, expect, it } from 'vitest';
import type { View } from '$lib/protocol';
import { reviewView, vote } from './fixtures';
import { answerGroups } from './groups';

const groupsOf = (view: View, category: number) => {
	const round = view.round!;
	return answerGroups(round.participants, round.result, round.proposals, category);
};

const merged = (voters: string[], category = 3, a = 'p1', b = 'p2') => ({
	category,
	playerA: a,
	playerB: b,
	proposedBy: a,
	votes: voters.map((voter) => ({ voter, yes: true }))
});

describe('answerGroups', () => {
	it('groups equal normalized answers (Fabián = fabian)', () => {
		const g = groupsOf(reviewView(), 0);
		expect(g.group('p1')).toEqual(['p1', 'p2']);
		expect(g.sharedWith('p1')).toBe(1);
		expect(g.same('p1', 'p2')).toBe(true);
		expect(g.same('p1', 'p3')).toBe(false);
	});

	it('leaves unique answers alone', () => {
		const g = groupsOf(reviewView(), 1);
		expect(g.sharedWith('p2')).toBe(0);
		expect(g.group('p2')).toEqual(['p2']);
		expect(g.sharedWith('p1')).toBe(1);
	});

	it('counts everybody who wrote the same thing', () => {
		const g = groupsOf(reviewView(), 4);
		expect(g.valid).toEqual(['p1', 'p2', 'p3', 'p4']);
		expect(g.sharedWith('p3')).toBe(3);
	});

	it('skips empty, wrong-letter and rejected answers', () => {
		const empty = groupsOf(reviewView(), 2);
		expect(empty.valid).toEqual(['p1', 'p2', 'p4']);
		expect(empty.group('p3')).toEqual([]);
		expect(empty.sharedWith('p3')).toBe(0);

		const wrong = groupsOf(reviewView(), 0);
		expect(wrong.valid).not.toContain('p4');

		const rejected = groupsOf(
			reviewView({ rejectVotes: [vote('p2', 'p3', 3), vote('p4', 'p3', 3)] }),
			3
		);
		expect(rejected.valid).toEqual(['p1', 'p2', 'p4']);
	});

	it('links the pairs of a passed proposal', () => {
		const g = groupsOf(reviewView({ proposals: [merged(['p1', 'p2', 'p3'])] }), 3);
		expect(g.same('p1', 'p2')).toBe(true);
		expect(g.sharedWith('p1')).toBe(1);
	});

	it('ignores proposals that have not passed or belong to another category', () => {
		const pending = groupsOf(reviewView({ proposals: [merged(['p1'])] }), 3);
		expect(pending.same('p1', 'p2')).toBe(false);
		const other = groupsOf(reviewView({ proposals: [merged(['p1', 'p2', 'p3'])] }), 1);
		expect(other.same('p1', 'p2')).toBe(false);
	});

	it('merges are transitive', () => {
		const view = reviewView({
			proposals: [
				merged(['p1', 'p2', 'p3'], 3, 'p1', 'p2'),
				merged(['p1', 'p2', 'p3'], 3, 'p2', 'p4')
			]
		});
		const g = groupsOf(view, 3);
		expect(g.group('p1')).toEqual(['p1', 'p2', 'p4']);
		expect(g.same('p1', 'p4')).toBe(true);
		expect(g.sharedWith('p4')).toBe(2);
	});

	it('a merge only links valid answers', () => {
		const g = groupsOf(reviewView({ proposals: [merged(['p1', 'p2', 'p3'], 0, 'p1', 'p4')] }), 0);
		expect(g.same('p1', 'p4')).toBe(false);
		expect(g.group('p4')).toEqual([]);
	});

	it('copes with a missing result', () => {
		const g = answerGroups(['p1', 'p2'], undefined, [], 0);
		expect(g.valid).toEqual([]);
		expect(g.sharedWith('p1')).toBe(0);
		expect(g.same('p1', 'p2')).toBe(false);
	});
});
