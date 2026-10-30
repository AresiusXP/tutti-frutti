import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import { reviewView } from '$lib/review/fixtures';
import type { Scenario } from './scenarios';

/**
 * The report / block sheet already open (the playground taps the first flag by itself), so that the
 * accessibility sweep and the screenshots see it: the menu of a player, of an answer, the list of players.
 */
function tapping(session: FakeSession): FakeSession {
	session.devTap = 'safety-button';
	return session;
}

const table = [player('p1', 'Ana'), player('p2', 'Beto'), player('p3', 'Caro')];

export const scenarios: Record<string, Scenario> = {
	'safety-lobby-player-menu': () =>
		tapping(new FakeSession(makeView({ youId: 'p1', hostId: 'p1', players: table }))),
	'safety-lobby-custom-label': () =>
		tapping(
			new FakeSession(
				makeView({
					youId: 'p2',
					hostId: 'p1',
					players: table,
					settings: makeSettings({
						categories: [{ id: 'nombre' }, { custom: 'Superhéroe' }, { id: 'pais' }]
					})
				})
			)
		),
	'safety-letter-players': () =>
		tapping(
			new FakeSession(
				makeView({
					phase: 'letter',
					youId: 'p1',
					chooserId: 'p2',
					roundNum: 1,
					players: table,
					round: makeRound({ participants: ['p1', 'p2', 'p3'] })
				})
			)
		),
	'safety-review-answer-menu': () =>
		tapping(new FakeSession(reviewView({ youId: 'p1', category: 1, judged: [1] })))
};
