import { describe, expect, it } from 'vitest';
import { player } from '$lib/game/fixtures';
import type { Player, RoundResult } from '$lib/protocol';
import { buildPodium } from './podium';

const p = (id: string, total: number, over: Partial<Player> = {}) =>
	player(id, id.toUpperCase(), { totalScore: total, joinedSeq: Number(id.slice(1)), ...over });

const round = (points: Record<string, number> = {}) => ({
	number: 3,
	letter: 'F',
	participants: Object.keys(points),
	reviewVoters: [],
	answers: {},
	filled: {},
	reviewCategory: 0,
	rejectVotes: [],
	proposals: [],
	judgePending: [],
	verdicts: [],
	result: {
		round: 3,
		letter: 'F',
		players: Object.fromEntries(
			Object.entries(points).map(([id, total]) => [id, { cells: [], total }])
		)
	} satisfies RoundResult
});

const shape = (podium: ReturnType<typeof buildPodium>) =>
	podium.places.map((place) => [place.place, place.medal, place.rows.map((r) => r.player.id)]);

describe('buildPodium', () => {
	it('puts the top three on gold, silver and bronze', () => {
		const podium = buildPodium({
			players: [p('p1', 30), p('p2', 50), p('p3', 40), p('p4', 10)],
			winners: ['p2'],
			round: round()
		});
		expect(shape(podium)).toEqual([
			[1, '🥇', ['p2']],
			[2, '🥈', ['p3']],
			[3, '🥉', ['p1']]
		]);
		expect(podium.winnerIds).toEqual(['p2']);
		expect(podium.ranking.map((r) => r.player.id)).toEqual(['p2', 'p3', 'p1', 'p4']);
	});

	it('a tie for first shares the gold step and leaves no silver', () => {
		const podium = buildPodium({
			players: [p('p1', 50), p('p2', 50), p('p3', 40), p('p4', 10)],
			winners: ['p1', 'p2'],
			round: round()
		});
		expect(shape(podium)).toEqual([
			[1, '🥇', ['p1', 'p2']],
			[3, '🥉', ['p3']]
		]);
		expect(podium.winnerIds).toEqual(['p1', 'p2']);
	});

	it('a tie for second shares silver and leaves no bronze', () => {
		const podium = buildPodium({
			players: [p('p1', 50), p('p2', 40), p('p3', 40), p('p4', 10)],
			winners: ['p1'],
			round: round()
		});
		expect(shape(podium)).toEqual([
			[1, '🥇', ['p1']],
			[2, '🥈', ['p2', 'p3']]
		]);
	});

	it('everybody tied is one big first place', () => {
		const podium = buildPodium({
			players: [p('p1', 10), p('p2', 10), p('p3', 10)],
			winners: ['p1', 'p2', 'p3'],
			round: round()
		});
		expect(shape(podium)).toEqual([[1, '🥇', ['p1', 'p2', 'p3']]]);
	});

	it('leaves out players who left, but keeps a winner declared by the server', () => {
		const podium = buildPodium({
			players: [p('p1', 30), p('p2', 99, { left: true }), p('p3', 20), p('p4', 90, { left: true })],
			winners: ['p2'],
			round: round()
		});
		expect(shape(podium)).toEqual([
			[1, '🥇', ['p2']],
			[2, '🥈', ['p1']],
			[3, '🥉', ['p3']]
		]);
		expect(podium.ranking.at(-1)).toMatchObject({ rank: null, left: true });
		expect(podium.ranking.at(-1)?.player.id).toBe('p4');
	});

	it('falls back to the first place when the server sent no winners', () => {
		const podium = buildPodium({ players: [p('p1', 5), p('p2', 9)], round: round() });
		expect(podium.winnerIds).toEqual(['p2']);
	});

	it('copes with nobody left', () => {
		const podium = buildPodium({
			players: [p('p1', 5, { left: true })],
			winners: [],
			round: undefined
		});
		expect(podium.places).toEqual([]);
		expect(podium.winnerIds).toEqual([]);
	});

	it('does not show round arrows: that is for the summary', () => {
		const podium = buildPodium({
			players: [p('p1', 30), p('p2', 20)],
			winners: ['p1'],
			round: round({ p1: 10, p2: 20 })
		});
		expect(podium.ranking.every((r) => r.change === null)).toBe(true);
	});
});
