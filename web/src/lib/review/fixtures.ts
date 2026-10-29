/**
 * Rich fixtures for the review / summary / final screens, tests and the dev
 * playground: a 4-player, 6-category round (letter F) with unique, shared,
 * empty, wrong-letter and rejected answers, merge proposals and a 3-round
 * history. `buildReviewView` recomputes the live result with a small mirror of
 * the engine (`scoring.go` + `review.go`), so every scenario is internally
 * consistent and the demo session can react to votes like the server does.
 */
import { DEFAULT_CATEGORY_IDS, makeSettings, makeView, player } from '$lib/game/fixtures';
import type {
	AIVerdict,
	Lang,
	MergeProposal,
	Player,
	ProposalView,
	RejectVote,
	RoundResult,
	RoundView,
	View
} from '$lib/protocol';
import { rejectTally } from './votes';

/** Fixture-only simulation of the server electorate. */
function electorate(participants: readonly string[], players: readonly Player[]): string[] {
	return participants.filter((id) => players.some((p) => p.id === id && p.connected && !p.left));
}

export const FIXTURE_LETTER = 'F';

export const LABELS: Record<Lang, string[]> = {
	es: ['Nombre', 'País', 'Animal', 'Comida', 'Color', 'Marca'],
	en: ['Name', 'Country', 'Animal', 'Food', 'Color', 'Brand'],
	nl: ['Naam', 'Land', 'Dier', 'Eten', 'Kleur', 'Merk']
};

/** Answers of the 4 players (p1..p4), per category, in the lobby language. */
export const ANSWERS: Record<Lang, Record<string, string[]>> = {
	es: {
		p1: ['Fabián', 'Francia', 'Foca', 'Frutilla', 'Fucsia', 'Ford'],
		p2: ['Fabián', 'Finlandia', 'Flamenco', 'Frutilas', 'Fucsia', 'Ferrari'],
		p3: ['Federico', 'Francia', '', 'Fierro', 'Fucsia', 'Fila'],
		p4: ['Pedro', 'Filipinas', 'Foca', 'Flan', 'Fucsia', 'Fanta']
	},
	en: {
		p1: ['Fred', 'France', 'Fox', 'French fries', 'Fuchsia', 'Ford'],
		p2: ['Fred', 'Finland', 'Flamingo', 'Fries', 'Fuchsia', 'Ferrari'],
		p3: ['Frank', 'France', '', 'Fork', 'Fuchsia', 'Fila'],
		p4: ['Peter', 'Fiji', 'Fox', 'Fudge', 'Fuchsia', 'Fanta']
	},
	nl: {
		p1: ['Femke', 'Frankrijk', 'Flamingo', 'Friet', 'Fuchsia', 'Ford'],
		p2: ['Femke', 'Finland', 'Fazant', 'Frieten', 'Fuchsia', 'Ferrari'],
		p3: ['Frank', 'Frankrijk', '', 'Fondue', 'Fuchsia', 'Fila'],
		p4: ['Peter', 'Filipijnen', 'Flamingo', 'Flan', 'Fuchsia', 'Fanta']
	}
};

const AVATARS = [
	{ emoji: '🍌', color: 'banana' },
	{ emoji: '🍊', color: 'orange' },
	{ emoji: '🍓', color: 'strawberry' },
	{ emoji: '🍇', color: 'grape' },
	{ emoji: '🍍', color: 'lime' }
];
const NAMES = [
	'Ana',
	'Beto',
	'Caro',
	'Dani',
	'Eli',
	'Fede',
	'Gabi',
	'Hugo',
	'Iris',
	'Juan',
	'Kira'
];

/** Totals before round 3, split in two earlier rounds (letters M and B). */
export const BASE_TOTALS: Record<string, number> = { p1: 30, p2: 55, p3: 35, p4: 50 };

export type FixturePhase = 'review' | 'round_summary' | 'final';

export interface ReviewState {
	phase: FixturePhase;
	lang: Lang;
	youId: string;
	hostId: string;
	category: number;
	roundNum: number;
	rounds: number;
	letter: string;
	players: Player[];
	participants: string[];
	answers: Record<string, string[]>;
	rejectVotes: RejectVote[];
	proposals: MergeProposal[];
	nextProposalId: number;
	/** The viewer may ask the AI judge (`state.canJudge`). */
	canJudge: boolean;
	/** Categories the AI is working on. */
	judgePending: number[];
	verdicts: AIVerdict[];
	/** Totals before this round. */
	baseTotals: Record<string, number>;
	/** The scored round, once the review is finished. */
	frozen: RoundResult | null;
	winners?: string[];
}

export interface ReviewOptions {
	phase?: FixturePhase;
	lang?: Lang;
	youId?: string;
	hostId?: string;
	category?: number;
	roundNum?: number;
	rounds?: number;
	rejectVotes?: RejectVote[];
	proposals?: Array<Omit<MergeProposal, 'id'> & { id?: number }>;
	/** The viewer is signed in and the server has an AI judge. */
	canJudge?: boolean;
	/** Categories the AI is working on right now. */
	judgePending?: number[];
	/** Categories that already carry (sample) verdicts. */
	judged?: number[];
	/** Explicit verdicts (added to the sample ones of `judged`). */
	verdicts?: AIVerdict[];
	/** Players without a socket. */
	offline?: string[];
	/** Players that left (seat flagged `left`). */
	left?: string[];
	/** Adds one more player who joined mid-round (watches; not a participant). */
	spectator?: boolean;
	/** Number of participants, 4 to 10 (default 4). */
	size?: number;
	/** Replaces answers: `{ p3: { 3: 'text' } }`. */
	answers?: Record<string, Record<number, string>>;
	/** Wanted totals after this round (the base is derived); used to build ties. */
	finalTotals?: Record<string, number>;
	winners?: string[];
}

export const vote = (voter: string, target: string, category: number): RejectVote => ({
	voter,
	target,
	category
});

export function reviewState(options: ReviewOptions = {}): ReviewState {
	const lang = options.lang ?? 'es';
	const size = Math.min(Math.max(options.size ?? 4, 4), 10);
	const ids = Array.from({ length: size }, (_, i) => `p${i + 1}`);
	const watcher = `p${size + 1}`;
	const answers: Record<string, string[]> = {};
	for (const [i, id] of ids.entries()) {
		// players beyond the fourth repeat the first four, with a twist so they stay distinct
		answers[id] = ANSWERS[lang][id]
			? [...ANSWERS[lang][id]]
			: ANSWERS[lang][ids[i % 4]].map((a, c) => (a && c !== 4 ? `${a}${'s'.repeat(i - 3)}` : a));
	}
	for (const [id, edits] of Object.entries(options.answers ?? {})) {
		for (const [c, text] of Object.entries(edits)) answers[id][Number(c)] = text;
	}
	const everyone = options.spectator ? [...ids, watcher] : ids;
	const players = everyone.map((id, i) =>
		player(id, NAMES[i], {
			avatar: AVATARS[i % AVATARS.length],
			joinedSeq: i + 1,
			joinedRound: id === watcher ? 4 : 1,
			connected: !(options.offline ?? []).includes(id),
			left: (options.left ?? []).includes(id)
		})
	);
	const state: ReviewState = {
		phase: options.phase ?? 'review',
		lang,
		youId: options.youId ?? (options.spectator ? watcher : 'p1'),
		hostId: options.hostId ?? 'p1',
		category: options.category ?? 1,
		roundNum: options.roundNum ?? 3,
		rounds: options.rounds ?? 5,
		letter: FIXTURE_LETTER,
		players,
		participants: ids,
		answers,
		rejectVotes: options.rejectVotes ?? [],
		proposals: (options.proposals ?? []).map((p, i) => ({ ...p, id: p.id ?? i + 1 })),
		nextProposalId: (options.proposals?.length ?? 0) + 1,
		canJudge: options.canJudge ?? false,
		judgePending: [...(options.judgePending ?? [])],
		verdicts: [...(options.verdicts ?? [])],
		baseTotals: Object.fromEntries(ids.map((id, i) => [id, BASE_TOTALS[id] ?? 20 + (i % 5) * 5])),
		frozen: null
	};
	for (const c of options.judged ?? []) state.verdicts.push(...sampleVerdicts(state, c));
	if (options.finalTotals) {
		const live = scoreState(state);
		for (const id of ids) {
			state.baseTotals[id] = Math.max(0, options.finalTotals[id] - (live.players[id]?.total ?? 0));
		}
	}
	if (state.phase === 'round_summary') freeze(state);
	else if (state.phase === 'final') {
		endGame(state);
		if (options.winners) state.winners = options.winners;
	}
	return state;
}

/** Scores the round and applies it to the totals, like `finish_review`. */
export function freeze(state: ReviewState): void {
	state.frozen = scoreState(state);
	if (state.phase === 'review') state.phase = 'round_summary';
}

export function endGame(state: ReviewState): void {
	if (!state.frozen) freeze(state);
	state.phase = 'final';
	const live = state.players.filter((p) => !p.left);
	const totals = new Map(live.map((p) => [p.id, totalOf(state, p.id)]));
	const best = Math.max(...totals.values());
	state.winners = live.filter((p) => totals.get(p.id) === best).map((p) => p.id);
}

function totalOf(state: ReviewState, id: string): number {
	return (state.baseTotals[id] ?? 0) + (state.frozen?.players[id]?.total ?? 0);
}

const DOUBT: Record<Lang, string> = {
	es: 'No estoy seguro de que exista',
	en: 'Not sure this is a real one',
	nl: 'Niet zeker of dit echt bestaat'
};
const VALID: Record<Lang, string> = {
	es: 'Respuesta conocida',
	en: 'A well-known answer',
	nl: 'Een bekend antwoord'
};

/**
 * Stand-in for the AI judge in the demo and the fixtures: one verdict per answer that is not empty and
 * starts with the letter (like the server), alternating valid / doubtful so both chips show up.
 */
export function sampleVerdicts(state: ReviewState, category: number): AIVerdict[] {
	const cells = scoreState(state).players;
	const verdicts: AIVerdict[] = [];
	for (const id of state.participants) {
		const status = cells[id]?.cells[category]?.status;
		if (status === undefined || status === 'empty' || status === 'wrong_letter') continue;
		const doubtful = verdicts.length % 2 === 1;
		verdicts.push({
			category,
			player: id,
			verdict: doubtful ? 'doubtful' : 'valid',
			reason: doubtful ? DOUBT[state.lang] : VALID[state.lang]
		});
	}
	return verdicts;
}

function norm(text: string, lang: Lang): string {
	const kept =
		lang === 'es' ? text.trim().toLowerCase().replaceAll('ñ', '\uE000') : text.toLowerCase();
	const plain = kept
		.replaceAll('ĳ', 'ij')
		.normalize('NFD')
		.replace(/\p{M}/gu, '')
		.replace(/\s+/g, ' ')
		.trim();
	return lang === 'es' ? plain.replaceAll('\uE000', 'ñ') : plain;
}

function passedIds(state: ReviewState): Set<number> {
	const online = electorate(state.participants, state.players);
	return new Set(
		state.proposals
			.filter(
				(p) => p.votes.filter((v) => v.yes && online.includes(v.voter)).length * 2 > online.length
			)
			.map((p) => p.id)
	);
}

/** Mirror of `ScoreRound` with the live rejections and merges. */
export function scoreState(state: ReviewState): RoundResult {
	const passed = passedIds(state);
	const categories = LABELS[state.lang].length;
	const players: RoundResult['players'] = {};
	for (const id of state.participants) players[id] = { cells: [], total: 0 };
	for (let c = 0; c < categories; c++) {
		const group = new Map<string, string>();
		const valid: string[] = [];
		for (const id of state.participants) {
			const raw = (state.answers[id]?.[c] ?? '').trim();
			const answer = norm(raw, state.lang);
			const rejected = rejectTally(
				electorate(state.participants, state.players),
				state.rejectVotes,
				c,
				id
			).rejected;
			let status: 'empty' | 'wrong_letter' | 'rejected' | 'valid' = 'valid';
			if (answer === '') status = 'empty';
			else if (!answer.startsWith(norm(state.letter, state.lang))) status = 'wrong_letter';
			else if (rejected) status = 'rejected';
			players[id].cells[c] = { raw, answer, status, points: 0, shared: false };
			if (status === 'valid') {
				valid.push(id);
				group.set(id, answer);
			}
		}
		const label = new Map<string, string>(valid.map((id) => [id, id]));
		const root = (id: string): string => {
			let r = id;
			while (label.get(r) !== r) r = label.get(r) as string;
			return r;
		};
		const join = (a: string, b: string) => label.set(root(a), root(b));
		const firstWith = new Map<string, string>();
		for (const id of valid) {
			const first = firstWith.get(group.get(id) as string);
			if (first) join(first, id);
			else firstWith.set(group.get(id) as string, id);
		}
		for (const pr of state.proposals) {
			if (
				pr.category === c &&
				passed.has(pr.id) &&
				label.has(pr.playerA) &&
				label.has(pr.playerB)
			) {
				join(pr.playerA, pr.playerB);
			}
		}
		for (const id of valid) {
			const cell = players[id].cells[c];
			cell.shared = valid.filter((o) => root(o) === root(id)).length > 1;
			cell.points = cell.shared ? 5 : 10;
		}
	}
	for (const id of state.participants) {
		players[id].total = players[id].cells.reduce((sum, cell) => sum + cell.points, 0);
	}
	return { round: state.roundNum, letter: state.letter, players };
}

function earlierRounds(state: ReviewState): RoundResult[] {
	const rounds: RoundResult[] = [];
	const letters = ['M', 'B', 'C', 'D'];
	const split = (total: number) => Math.round(total / 10) * 5;
	for (let r = 1; r < state.roundNum; r++) {
		const result: RoundResult = { round: r, letter: letters[r - 1] ?? 'A', players: {} };
		for (const id of state.participants) {
			const base = state.baseTotals[id] ?? 0;
			const first = split(base);
			result.players[id] = { cells: [], total: r === 1 ? first : base - first };
		}
		rounds.push(result);
	}
	return rounds;
}

/** The `View` the server would send to `state.youId`. */
export function buildReviewView(state: ReviewState): View {
	const live = state.frozen ?? scoreState(state);
	const online = electorate(state.participants, state.players);
	const proposals: ProposalView[] = state.proposals.map((p) => {
		const yes = p.votes.filter((v) => v.yes && online.includes(v.voter)).length;
		return { ...p, votes: p.votes.map((v) => ({ ...v })), yes, passed: yes * 2 > online.length };
	});
	const round: RoundView = {
		number: state.roundNum,
		letter: state.letter,
		participants: [...state.participants],
		reviewVoters: state.phase === 'review' ? online : [],
		answers: Object.fromEntries(state.participants.map((id) => [id, [...state.answers[id]]])),
		filled: Object.fromEntries(
			state.participants.map((id) => [id, state.answers[id].filter((a) => a !== '').length])
		),
		reviewCategory: state.category,
		rejectVotes: state.phase === 'review' ? [...state.rejectVotes] : [],
		proposals: state.phase === 'review' ? proposals : [],
		judgePending: state.phase === 'review' ? [...state.judgePending] : [],
		verdicts: state.phase === 'review' ? state.verdicts.map((v) => ({ ...v })) : [],
		result: live
	};
	const scored = state.phase !== 'review';
	const players = state.players.map((p) => ({
		...p,
		totalScore: (state.baseTotals[p.id] ?? 0) + (scored ? (live.players[p.id]?.total ?? 0) : 0)
	}));
	const settings = makeSettings({
		lang: state.lang,
		rounds: state.rounds,
		categories: DEFAULT_CATEGORY_IDS.map((id) => ({ id }))
	});
	return makeView({
		phase: state.phase,
		youId: state.youId,
		hostId: state.hostId,
		players,
		settings,
		categoryLabels: LABELS[state.lang],
		usedLetters: ['M', 'B', state.letter].slice(-state.roundNum),
		roundNum: state.roundNum,
		round,
		history: scored ? [...earlierRounds(state), live] : earlierRounds(state),
		winners: state.phase === 'final' ? state.winners : undefined
	});
}

/** A ready `View` in one call. */
export function reviewView(options: ReviewOptions = {}): View {
	return buildReviewView(reviewState(options));
}
