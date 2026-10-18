import { describe, expect, it } from 'vitest';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import { badgeAvatar, playerProgress } from './progress';

function view() {
	const players = [
		player('p1', 'Ana'),
		player('p2', 'Beto', { connected: false }),
		player('p3', 'Caro'),
		player('p4', 'Dani', { left: true }),
		player('p5', 'Eli')
	];
	return makeView({
		phase: 'playing',
		players,
		youId: 'p1',
		settings: makeSettings(),
		round: makeRound({
			letter: 'F',
			participants: ['p1', 'p2', 'p3', 'p4'],
			filled: { p1: 1, p2: 3, p3: 6, p4: 2 }
		})
	});
}

describe('playerProgress', () => {
	it('lists the participants still in the game, in join order', () => {
		const list = playerProgress(view(), 0);
		expect(list.map((e) => e.id)).toEqual(['p1', 'p2', 'p3']);
	});

	it('uses the local count for the viewer and round.filled for the others', () => {
		const list = playerProgress(view(), 4);
		expect(list.map((e) => [e.id, e.filled, e.total, e.you])).toEqual([
			['p1', 4, 6, true],
			['p2', 3, 6, false],
			['p3', 6, 6, false]
		]);
	});

	it('flags offline players, completed ones and computes the percentage', () => {
		const [me, beto, caro] = playerProgress(view(), 3);
		expect(beto.offline).toBe(true);
		expect(me.offline).toBe(false);
		expect(caro.done).toBe(true);
		expect(me.done).toBe(false);
		expect(me.percent).toBe(50);
		expect(caro.percent).toBe(100);
	});

	it('clamps the counts to the number of categories', () => {
		const list = playerProgress(view(), 99);
		expect(list[0].filled).toBe(6);
	});

	it('has no viewer entry for a spectator and tolerates a missing count', () => {
		const v = view();
		v.youId = 'p5';
		const list = playerProgress(v, 0);
		expect(list.some((e) => e.you)).toBe(false);
		v.round!.filled = {};
		expect(playerProgress(v, 0).every((e) => e.filled === 0)).toBe(true);
	});

	it('is empty without a round', () => {
		expect(playerProgress(makeView(), 0)).toEqual([]);
	});

	it('maps the wire avatar to the badge avatar', () => {
		expect(badgeAvatar({ emoji: '🍓', color: 'strawberry' })).toEqual({
			emoji: '🍓',
			color: 'strawberry'
		});
	});
});
