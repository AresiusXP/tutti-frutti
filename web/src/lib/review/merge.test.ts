import { describe, expect, it } from 'vitest';
import { reviewView, vote } from './fixtures';
import {
	firstPickable,
	MERGE_IDLE,
	pairBlocker,
	stepMerge,
	type MergeContext,
	type MergeState
} from './merge';

const ctxOf = (options: Parameters<typeof reviewView>[0], category: number): MergeContext => {
	const round = reviewView(options).round!;
	return {
		category,
		participants: round.participants,
		result: round.result,
		proposals: round.proposals
	};
};

const pending = (a: string, b: string, category = 3, voters = ['p1']) => ({
	category,
	playerA: a,
	playerB: b,
	proposedBy: a,
	votes: voters.map((voter) => ({ voter, yes: true }))
});

describe('pairBlocker', () => {
	it('allows two different valid answers', () => {
		expect(pairBlocker(ctxOf({}, 3), 'p1', 'p2')).toBeNull();
		expect(pairBlocker(ctxOf({}, 1), 'p2', 'p4')).toBeNull();
	});

	it('refuses the same player (invalid_merge)', () => {
		expect(pairBlocker(ctxOf({}, 3), 'p1', 'p1')).toBe('same_player');
	});

	it('refuses empty, wrong-letter and rejected answers (invalid_merge)', () => {
		expect(pairBlocker(ctxOf({}, 2), 'p1', 'p3')).toBe('invalid');
		expect(pairBlocker(ctxOf({}, 0), 'p1', 'p4')).toBe('invalid');
		const rejected = ctxOf({ rejectVotes: [vote('p2', 'p3', 3), vote('p4', 'p3', 3)] }, 3);
		expect(pairBlocker(rejected, 'p1', 'p3')).toBe('invalid');
	});

	it('refuses answers that already count the same (already_same)', () => {
		expect(pairBlocker(ctxOf({}, 0), 'p1', 'p2')).toBe('already_same');
		const passed = ctxOf({ proposals: [pending('p1', 'p2', 3, ['p1', 'p2', 'p3'])] }, 3);
		expect(pairBlocker(passed, 'p2', 'p1')).toBe('already_same');
	});

	it('sees merges that are transitive', () => {
		const ctx = ctxOf(
			{
				proposals: [
					pending('p1', 'p2', 3, ['p1', 'p2', 'p3']),
					pending('p2', 'p4', 3, ['p1', 'p2', 'p3'])
				]
			},
			3
		);
		expect(pairBlocker(ctx, 'p1', 'p4')).toBe('already_same');
	});

	it('refuses a pair with an open proposal, in either order (proposal_exists)', () => {
		const ctx = ctxOf({ proposals: [pending('p1', 'p2')] }, 3);
		expect(pairBlocker(ctx, 'p1', 'p2')).toBe('proposal_exists');
		expect(pairBlocker(ctx, 'p2', 'p1')).toBe('proposal_exists');
		expect(pairBlocker(ctx, 'p1', 'p4')).toBeNull();
	});

	it('an open proposal of another category does not block', () => {
		expect(pairBlocker(ctxOf({ proposals: [pending('p1', 'p2', 3)] }, 5), 'p1', 'p2')).toBeNull();
	});
});

describe('firstPickable', () => {
	it('lists the players with a possible twin', () => {
		expect(firstPickable(ctxOf({}, 3))).toEqual(['p1', 'p2', 'p3', 'p4']);
		expect(firstPickable(ctxOf({}, 2))).toEqual(['p1', 'p2', 'p4']);
	});

	it('is empty when every valid answer is already the same', () => {
		expect(firstPickable(ctxOf({}, 4))).toEqual([]);
	});

	it('is empty when only one answer is valid', () => {
		const ctx = ctxOf({ answers: { p2: { 5: '' }, p3: { 5: '' }, p4: { 5: '' } } }, 5);
		expect(firstPickable(ctx)).toEqual([]);
	});

	it('drops players whose only partner is blocked by an open proposal', () => {
		const ctx = ctxOf(
			{ answers: { p3: { 3: '' }, p4: { 3: '' } }, proposals: [pending('p1', 'p2')] },
			3
		);
		expect(firstPickable(ctx)).toEqual([]);
	});
});

describe('stepMerge', () => {
	// Animal: Foca, Flamenco, (empty), Foca
	const ctx = ctxOf({}, 2);
	const run = (state: MergeState, ...players: string[]) => {
		let current = state;
		let proposed: { a: string; b: string } | undefined;
		for (const player of players) {
			const next = stepMerge(current, { type: 'pick', player }, ctx);
			current = next.state;
			proposed = next.propose ?? proposed;
		}
		return { state: current, proposed };
	};

	it('toggle starts and stops the selection', () => {
		const started = stepMerge(MERGE_IDLE, { type: 'toggle' }, ctx);
		expect(started.state).toEqual({ step: 'first' });
		expect(stepMerge(started.state, { type: 'toggle' }, ctx).state).toEqual(MERGE_IDLE);
		expect(stepMerge({ step: 'second', first: 'p1' }, { type: 'toggle' }, ctx).state).toEqual(
			MERGE_IDLE
		);
	});

	it('cancel always goes back to idle', () => {
		expect(stepMerge({ step: 'second', first: 'p1' }, { type: 'cancel' }, ctx).state).toEqual(
			MERGE_IDLE
		);
		expect(stepMerge({ step: 'first' }, { type: 'cancel' }, ctx).state).toEqual(MERGE_IDLE);
	});

	it('picking while idle does nothing', () => {
		expect(stepMerge(MERGE_IDLE, { type: 'pick', player: 'p1' }, ctx)).toEqual({
			state: MERGE_IDLE
		});
	});

	it('first pick remembers the answer, second pick proposes the pair', () => {
		const first = run({ step: 'first' }, 'p1');
		expect(first.state).toEqual({ step: 'second', first: 'p1' });
		expect(first.proposed).toBeUndefined();
		const second = run({ step: 'first' }, 'p1', 'p2');
		expect(second.state).toEqual(MERGE_IDLE);
		expect(second.proposed).toEqual({ a: 'p1', b: 'p2' });
	});

	it('cannot pick an answer that cannot be merged first', () => {
		expect(run({ step: 'first' }, 'p3').state).toEqual({ step: 'first' });
	});

	it('picking the first answer again goes back to choosing the first', () => {
		expect(run({ step: 'first' }, 'p1', 'p1').state).toEqual({ step: 'first' });
	});

	it('ignores a second pick that is not valid and keeps waiting', () => {
		for (const other of ['p3', 'p4']) {
			const blocked = run({ step: 'first' }, 'p1', other);
			expect(blocked.state).toEqual({ step: 'second', first: 'p1' });
			expect(blocked.proposed).toBeUndefined();
		}
	});

	it('does not propose a pair that already has an open proposal', () => {
		const withOpen = ctxOf({ proposals: [pending('p1', 'p2')] }, 3);
		const next = stepMerge(
			{ step: 'second', first: 'p1' },
			{ type: 'pick', player: 'p2' },
			withOpen
		);
		expect(next).toEqual({ state: { step: 'second', first: 'p1' } });
	});
});
