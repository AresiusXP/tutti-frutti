/**
 * Playground scenarios of the review, summary and final screens
 * (`/dev?s=review-host&lang=es&bare=1`). Every scenario also exists as
 * `<name>-en` with an English lobby (English answers and category labels);
 * the UI language is chosen separately with `&lang=`.
 */
import { ReviewDemoSession } from '$lib/review/demo';
import { reviewState, vote, type ReviewOptions } from '$lib/review/fixtures';
import type { Scenario } from './scenarios';

const COMIDA = 3;
const rejectCaro = [vote('p2', 'p3', COMIDA), vote('p4', 'p3', COMIDA)];
const frutilla = (votes: Array<{ voter: string; yes: boolean }>) => ({
	category: COMIDA,
	playerA: 'p1',
	playerB: 'p2',
	proposedBy: 'p1',
	votes
});

const definitions: Record<string, ReviewOptions> = {
	'review-host': {
		category: COMIDA,
		rejectVotes: rejectCaro,
		proposals: [frutilla([{ voter: 'p1', yes: true }])]
	},
	'review-guest': {
		youId: 'p2',
		category: 1,
		rejectVotes: [vote('p4', 'p1', 1)]
	},
	'review-voting': {
		category: COMIDA,
		rejectVotes: [vote('p2', 'p3', COMIDA)]
	},
	'review-merge-pending': {
		youId: 'p3',
		category: COMIDA,
		proposals: [
			frutilla([
				{ voter: 'p1', yes: true },
				{ voter: 'p4', yes: false }
			])
		]
	},
	'review-merge-passed': {
		youId: 'p4',
		category: COMIDA,
		proposals: [
			frutilla([
				{ voter: 'p1', yes: true },
				{ voter: 'p2', yes: true },
				{ voter: 'p3', yes: true }
			])
		]
	},
	'review-last-category': { category: 5 },
	'review-offline': {
		category: COMIDA,
		offline: ['p4'],
		rejectVotes: [vote('p2', 'p3', COMIDA)]
	},
	'review-spectator': { spectator: true, category: 1 },
	'review-long': {
		category: COMIDA,
		answers: {
			p3: { [COMIDA]: 'Fideos caseros con salsa de hongos y ajo' },
			p4: { [COMIDA]: 'F' + 'a'.repeat(39) }
		}
	},
	'review-ten': { size: 10, category: 1, rejectVotes: [vote('p2', 'p1', 1)] },
	'summary-host': {
		phase: 'round_summary',
		rejectVotes: rejectCaro,
		proposals: [
			frutilla([
				{ voter: 'p1', yes: true },
				{ voter: 'p2', yes: true },
				{ voter: 'p4', yes: true }
			])
		]
	},
	'summary-guest': { phase: 'round_summary', youId: 'p3', rejectVotes: rejectCaro },
	'summary-last-round': { phase: 'round_summary', rounds: 3, rejectVotes: rejectCaro },
	'summary-spectator': { phase: 'round_summary', spectator: true, rejectVotes: rejectCaro },
	'summary-ten': { phase: 'round_summary', size: 10 },
	'final-ten': { phase: 'final', size: 10 },
	'final-host': { phase: 'final', rejectVotes: rejectCaro },
	'final-guest': { phase: 'final', youId: 'p3', rejectVotes: rejectCaro },
	'final-tie': {
		phase: 'final',
		rejectVotes: rejectCaro,
		finalTotals: { p1: 105, p2: 105, p3: 80, p4: 75 }
	},
	'final-tie-all': {
		phase: 'final',
		rejectVotes: rejectCaro,
		finalTotals: { p1: 100, p2: 100, p3: 100, p4: 100 }
	},
	'final-left-winner': {
		phase: 'final',
		youId: 'p3',
		rejectVotes: rejectCaro,
		left: ['p2'],
		winners: ['p2']
	}
};

export const scenarios: Record<string, Scenario> = {};
for (const [name, options] of Object.entries(definitions)) {
	scenarios[name] = () => new ReviewDemoSession(reviewState(options));
	scenarios[`${name}-en`] = () => new ReviewDemoSession(reviewState({ ...options, lang: 'en' }));
}
