/**
 * Builders for `View` snapshots in tests and the dev playground. They produce
 * VALID, minimal objects (like the server would) that you tweak per test:
 *
 *   const v = makeView({ youId: 'p1', hostId: 'p1', players: [player('p1','Ana'), player('p2','Beto')] });
 *   const playing = makeView({ phase: 'playing', round: makeRound({ letter: 'F' }) });
 */
import type { Phase, Player, RoundView, Settings, StopVoteView, View } from '$lib/protocol';

export const DEFAULT_CATEGORY_IDS = ['nombre', 'pais', 'animal', 'comida', 'color', 'marca'];
export const DEFAULT_CATEGORY_LABELS = ['Nombre', 'País', 'Animal', 'Comida', 'Color', 'Marca'];

export function player(id: string, name: string, over: Partial<Player> = {}): Player {
	return {
		id,
		name,
		avatar: { emoji: '🍓', color: 'strawberry' },
		connected: true,
		connectedAt: '2026-01-01T12:00:00Z',
		joinedSeq: Number(id.replace(/\D/g, '')) || 1,
		joinedRound: 1,
		left: false,
		totalScore: 0,
		...over
	};
}

export function makeSettings(over: Partial<Settings> = {}): Settings {
	return {
		lang: 'es',
		excludeHardLetters: true,
		rounds: 5,
		categories: DEFAULT_CATEGORY_IDS.map((id) => ({ id })),
		timeLimitSeconds: 0,
		stopVote: true,
		...over
	};
}

/** An open vote to stop (`round.stopVote`); `voters` default to everybody connected but the asker. */
export function makeStopVote(over: Partial<StopVoteView> = {}): StopVoteView {
	return {
		callerId: 'p1',
		endsAt: '2026-01-01T12:00:15Z',
		voters: ['p2', 'p3'],
		yes: [],
		...over
	};
}

export function makeRound(over: Partial<RoundView> = {}): RoundView {
	return {
		number: 1,
		letter: '',
		participants: [],
		answers: {},
		filled: {},
		reviewCategory: 0,
		rejectVotes: [],
		proposals: [],
		judgePending: [],
		verdicts: [],
		...over
	};
}

export function makeView(over: Partial<View> = {}): View {
	const players = over.players ?? [player('p1', 'Ana'), player('p2', 'Beto'), player('p3', 'Caro')];
	const settings = over.settings ?? makeSettings();
	return {
		phase: 'lobby' as Phase,
		youId: players[0].id,
		hostId: players[0].id,
		maxPlayers: 10,
		players,
		settings,
		categoryLabels: settings.categories.map(
			(c, i) => c.custom ?? DEFAULT_CATEGORY_LABELS[i] ?? c.id ?? `#${i}`
		),
		usedLetters: [],
		roundNum: 0,
		history: [],
		...over
	};
}
