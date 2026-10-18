import { describe, expect, it } from 'vitest';
import { player } from '$lib/game/fixtures';
import type { Player, RoundResult } from '$lib/protocol';
import { buildHistory, buildRanking } from './ranking';

const p = (id: string, total: number, over: Partial<Player> = {}) =>
	player(id, id.toUpperCase(), { totalScore: total, joinedSeq: Number(id.slice(1)), ...over });

const result = (points: Record<string, number>, round = 2): RoundResult => ({
	round,
	letter: 'F',
	players: Object.fromEntries(
		Object.entries(points).map(([id, total]) => [id, { cells: [], total }])
	)
});

describe('buildRanking', () => {
	it('sorts by running total, best first', () => {
		const rows = buildRanking([p('p1', 10), p('p2', 30), p('p3', 20)], undefined);
		expect(rows.map((r) => [r.player.id, r.rank])).toEqual([
			['p2', 1],
			['p3', 2],
			['p1', 3]
		]);
	});

	it('ties share a rank and the next one skips (1, 1, 3), ordered by join order', () => {
		const rows = buildRanking([p('p3', 20), p('p1', 20), p('p2', 5), p('p4', 20)], undefined);
		expect(rows.map((r) => [r.player.id, r.rank])).toEqual([
			['p1', 1],
			['p3', 1],
			['p4', 1],
			['p2', 4]
		]);
	});

	it("reports this round's points and whether the player took part", () => {
		const rows = buildRanking([p('p1', 40), p('p2', 25), p('p3', 0)], result({ p1: 25, p2: 25 }));
		const byId = Object.fromEntries(rows.map((r) => [r.player.id, r]));
		expect(byId.p1).toMatchObject({ roundPoints: 25, played: true, total: 40 });
		expect(byId.p3).toMatchObject({ roundPoints: 0, played: false, total: 0 });
	});

	it('players who left go last, unranked and greyed', () => {
		const rows = buildRanking([p('p1', 10), p('p2', 99, { left: true }), p('p3', 20)], undefined);
		expect(rows.map((r) => [r.player.id, r.rank, r.left])).toEqual([
			['p3', 1, false],
			['p1', 2, false],
			['p2', null, true]
		]);
	});

	it('keeps listed players who left in the ranking (declared winners)', () => {
		const rows = buildRanking([p('p1', 10), p('p2', 99, { left: true })], undefined, {
			keep: ['p2']
		});
		expect(rows.map((r) => [r.player.id, r.rank, r.left])).toEqual([
			['p2', 1, true],
			['p1', 2, false]
		]);
	});

	it('does not compare with the previous standings unless asked', () => {
		const rows = buildRanking([p('p1', 40), p('p2', 25)], result({ p1: 5, p2: 25 }));
		expect(rows.every((r) => r.change === null)).toBe(true);
	});

	it('computes places gained and lost against the standings before the round', () => {
		// before: p1 35, p2 30, p3 0  ->  after: p2 55, p1 40, p3 20
		const rows = buildRanking(
			[p('p1', 40), p('p2', 55), p('p3', 20)],
			result({ p1: 5, p2: 25, p3: 20 }),
			{ compare: true }
		);
		const change = Object.fromEntries(rows.map((r) => [r.player.id, r.change]));
		expect(change).toEqual({ p2: 1, p1: -1, p3: 0 });
	});

	it('handles players that joined this round (nothing before) and players that left', () => {
		const rows = buildRanking(
			[p('p1', 30), p('p2', 20), p('p3', 10, { left: true })],
			result({ p1: 10, p2: 20 }),
			{ compare: true }
		);
		expect(rows.find((r) => r.player.id === 'p3')?.change).toBeNull();
		// before: p1 20, p2 0 -> p2 climbed nothing: still second
		expect(rows.find((r) => r.player.id === 'p2')?.change).toBe(0);
	});
});

describe('buildHistory', () => {
	const players = [p('p1', 0), p('p2', 0), p('p3', 0, { left: true })];

	it('lists every round with its players best first', () => {
		const rounds = buildHistory(
			[result({ p1: 10, p2: 30, p3: 20 }, 1), result({ p1: 15, p2: 15, p3: 0 }, 2)],
			players
		);
		expect(rounds.map((r) => [r.round, r.letter, r.best])).toEqual([
			[1, 'F', 30],
			[2, 'F', 15]
		]);
		expect(rounds[0].entries.map((e) => [e.player.id, e.total])).toEqual([
			['p2', 30],
			['p3', 20],
			['p1', 10]
		]);
		expect(rounds[1].entries.map((e) => e.player.id)).toEqual(['p1', 'p2', 'p3']);
	});

	it('skips players that took no part and unknown ids', () => {
		const [round] = buildHistory([result({ p1: 10, ghost: 99 })], players);
		expect(round.entries.map((e) => e.player.id)).toEqual(['p1']);
	});

	it('copes with an empty round and an empty history', () => {
		expect(buildHistory([], players)).toEqual([]);
		expect(buildHistory([result({})], players)[0]).toMatchObject({ entries: [], best: 0 });
	});
});
