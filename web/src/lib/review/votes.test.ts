import { describe, expect, it } from 'vitest';
import { player } from '$lib/game/fixtures';
import type { Player, ProposalView, RejectVote } from '$lib/protocol';
import { electorate, majority, mergeTally, rejectTally } from './votes';

const ids = (n: number) => Array.from({ length: n }, (_, i) => `p${i + 1}`);
const seats = (n: number, offline: string[] = [], left: string[] = []): Player[] =>
	ids(n).map((id) =>
		player(id, id.toUpperCase(), { connected: !offline.includes(id), left: left.includes(id) })
	);
const votes = (voters: string[], target = 'p1', category = 0): RejectVote[] =>
	voters.map((voter) => ({ voter, target, category }));

describe('majority', () => {
	it('is the smallest strict majority', () => {
		expect([0, 1, 2, 3, 4, 5].map(majority)).toEqual([1, 1, 2, 2, 3, 3]);
	});
	it('never goes below one', () => {
		expect(majority(-3)).toBe(1);
	});
});

describe('electorate', () => {
	it('keeps connected participants that did not leave, in participant order', () => {
		const players = seats(5, ['p2'], ['p4']);
		expect(electorate(['p5', 'p4', 'p3', 'p2', 'p1'], players)).toEqual(['p5', 'p3', 'p1']);
	});
	it('ignores players that are not participants (spectators)', () => {
		expect(electorate(['p1', 'p2'], seats(4))).toEqual(['p1', 'p2']);
	});
});

// Same table as TestRejectThresholds in server/internal/game/review_test.go.
describe('rejectTally mirrors the server thresholds', () => {
	const table: Array<[string, number, string[], string[], boolean]> = [
		['2 players: 0 votes', 2, [], [], false],
		['2 players: the only other player votes', 2, ['p2'], [], true],
		['3 players: 1 of 2 is not more than half', 3, ['p2'], [], false],
		['3 players: 2 of 2', 3, ['p2', 'p3'], [], true],
		['4 players: 1 of 3', 4, ['p2'], [], false],
		['4 players: 2 of 3', 4, ['p2', 'p3'], [], true],
		['4 players: 3 of 3', 4, ['p2', 'p3', 'p4'], [], true],
		['5 players: 2 of 4 is exactly half', 5, ['p2', 'p3'], [], false],
		['5 players: 3 of 4', 5, ['p2', 'p3', 'p4'], [], true],
		['3 players, 1 disconnected: 1 of 1', 3, ['p2'], ['p3'], true],
		['3 players, 1 disconnected: 0 of 1', 3, [], ['p3'], false],
		['4 players, 1 disconnected: 1 of 2 is exactly half', 4, ['p2'], ['p4'], false],
		['4 players, 1 disconnected: 2 of 2', 4, ['p2', 'p3'], ['p4'], true],
		['5 players, 2 disconnected: 1 of 2', 5, ['p2'], ['p4', 'p5'], false],
		['5 players, 2 disconnected: 2 of 2', 5, ['p2', 'p3'], ['p4', 'p5'], true],
		['votes from players who disconnected are ignored', 3, ['p3'], ['p3'], false],
		['a disconnected voter does not stop the others', 3, ['p2', 'p3'], ['p3'], true],
		['the author being disconnected does not shrink the electorate', 3, ['p2'], ['p1'], false],
		['the author being disconnected, everyone else votes', 3, ['p2', 'p3'], ['p1'], true]
	];
	for (const [name, n, voters, offline, want] of table) {
		it(name, () => {
			const tally = rejectTally(ids(n), seats(n, offline), votes(voters), 0, 'p1', 'p2');
			expect(tally.rejected).toBe(want);
		});
	}
});

describe('rejectTally details', () => {
	it('counts votes against the votes needed', () => {
		const tally = rejectTally(ids(4), seats(4), votes(['p2']), 0, 'p1', 'p3');
		expect(tally).toMatchObject({
			votes: 1,
			needed: 2,
			eligible: 3,
			voters: ['p2'],
			mine: false,
			rejected: false
		});
	});

	it('flags my own vote', () => {
		expect(rejectTally(ids(4), seats(4), votes(['p3']), 0, 'p1', 'p3').mine).toBe(true);
	});

	it('keeps my vote visible even when it does not count', () => {
		const tally = rejectTally(ids(3), seats(3, ['p3']), votes(['p3']), 0, 'p1', 'p3');
		expect(tally.mine).toBe(true);
		expect(tally.votes).toBe(0);
	});

	it("never counts the author's own vote", () => {
		const tally = rejectTally(ids(3), seats(3), votes(['p1']), 0, 'p1');
		expect(tally.votes).toBe(0);
		expect(tally.rejected).toBe(false);
	});

	it('only looks at the target and the category asked for', () => {
		const all = [
			...votes(['p2', 'p3'], 'p1', 1),
			...votes(['p2', 'p3'], 'p4', 0),
			...votes(['p3'], 'p1', 0)
		];
		const tally = rejectTally(ids(4), seats(4), all, 0, 'p1');
		expect(tally.voters).toEqual(['p3']);
	});

	it('ignores votes of non-participants and players who left', () => {
		const players = seats(5, [], ['p4']);
		const tally = rejectTally(
			['p1', 'p2', 'p3', 'p4'],
			players,
			votes(['p4', 'p5', 'p2']),
			0,
			'p1'
		);
		expect(tally.voters).toEqual(['p2']);
		expect(tally.eligible).toBe(2);
	});

	it('does not count the same voter twice', () => {
		const tally = rejectTally(ids(4), seats(4), votes(['p2', 'p2']), 0, 'p1');
		expect(tally.votes).toBe(1);
	});

	it('has nobody to convince when the author is alone', () => {
		const tally = rejectTally(['p1'], seats(1), [], 0, 'p1');
		expect(tally).toMatchObject({ eligible: 0, needed: 1, rejected: false });
	});

	it('works without a viewer', () => {
		expect(rejectTally(ids(3), seats(3), votes(['p2']), 0, 'p1').mine).toBe(false);
	});
});

describe('mergeTally', () => {
	const proposal = (over: Partial<ProposalView> = {}): ProposalView => ({
		id: 1,
		category: 0,
		playerA: 'p1',
		playerB: 'p2',
		proposedBy: 'p1',
		votes: [{ voter: 'p1', yes: true }],
		yes: 1,
		passed: false,
		...over
	});

	// Same table as TestMergeThresholdsByPlayerCount in the Go tests.
	for (const [n, yes, want] of [
		[2, 1, false],
		[2, 2, true],
		[3, 1, false],
		[3, 2, true],
		[4, 2, false],
		[4, 3, true],
		[5, 2, false],
		[5, 3, true]
	] as const) {
		it(`${n} players need a majority: ${yes} yes ${want ? 'passes' : 'does not pass'}`, () => {
			const tally = mergeTally(proposal({ yes, passed: yes * 2 > n }), ids(n), seats(n));
			expect(tally.total).toBe(n);
			expect(tally.needed).toBe(majority(n));
			expect(tally.passed).toBe(want);
			expect(tally.yes * 2 > tally.total).toBe(want);
		});
	}

	it('uses the connected participants as the electorate', () => {
		const tally = mergeTally(proposal(), ids(4), seats(4, ['p4']), 'p2');
		expect(tally.total).toBe(3);
		expect(tally.needed).toBe(2);
	});

	it('reports my own vote, yes or no, or none', () => {
		const p = proposal({
			votes: [
				{ voter: 'p1', yes: true },
				{ voter: 'p2', yes: false }
			]
		});
		expect(mergeTally(p, ids(3), seats(3), 'p1').mine).toBe(true);
		expect(mergeTally(p, ids(3), seats(3), 'p2').mine).toBe(false);
		expect(mergeTally(p, ids(3), seats(3), 'p3').mine).toBeNull();
		expect(mergeTally(p, ids(3), seats(3)).mine).toBeNull();
	});
});
