/**
 * "Son iguales / Same": choosing two answers of a category to propose a merge.
 *
 * The screen keeps a `MergeState` and feeds it `MergeAction`s; the pure
 * `stepMerge` says what the next state is and, when the second answer was
 * picked, which pair to propose. `pairBlocker` mirrors the checks of
 * `proposeMerge` in `server/internal/game/review.go` so the UI never sends an
 * obviously invalid proposal (`invalid_merge`, `already_same`,
 * `proposal_exists`).
 */
import type { ProposalView, RoundResult } from '$lib/protocol';
import { answerGroups } from './groups';

export type MergeState =
	| { step: 'idle' }
	/** Waiting for the first answer. */
	| { step: 'first' }
	/** First answer chosen, waiting for its twin. */
	| { step: 'second'; first: string };

export const MERGE_IDLE: MergeState = { step: 'idle' };

export type MergeAction =
	{ type: 'toggle' } | { type: 'cancel' } | { type: 'pick'; player: string };

/** How the merge selection looks on one answer card. */
export type MergeUi =
	| { kind: 'none' }
	/** Can be picked (first or second). */
	| { kind: 'pick' }
	/** The first answer already chosen (tap to undo). */
	| { kind: 'picked' }
	/** Cannot take part (empty, rejected, wrong letter…). */
	| { kind: 'dim' }
	/** Valid but cannot be paired with the first answer. */
	| { kind: 'note'; note: 'already' | 'pending' };

export type PairBlocker = 'same_player' | 'invalid' | 'already_same' | 'proposal_exists';

export interface MergeContext {
	category: number;
	participants: readonly string[];
	result: RoundResult | undefined;
	proposals: readonly ProposalView[];
}

/** Why `a` and `b` cannot be proposed as the same, or `null` when they can. */
export function pairBlocker(ctx: MergeContext, a: string, b: string): PairBlocker | null {
	if (a === b) return 'same_player';
	const groups = answerGroups(ctx.participants, ctx.result, ctx.proposals, ctx.category);
	if (!groups.valid.includes(a) || !groups.valid.includes(b)) return 'invalid';
	if (groups.same(a, b)) return 'already_same';
	const pending = ctx.proposals.some(
		(pr) =>
			pr.category === ctx.category &&
			!pr.passed &&
			((pr.playerA === a && pr.playerB === b) || (pr.playerA === b && pr.playerB === a))
	);
	return pending ? 'proposal_exists' : null;
}

/** Players whose answer can be chosen first: valid and with at least one possible twin. */
export function firstPickable(ctx: MergeContext): string[] {
	return ctx.participants.filter((id) =>
		ctx.participants.some((other) => pairBlocker(ctx, id, other) === null)
	);
}

export interface MergeStep {
	state: MergeState;
	/** Set when the pick completed a pair: send `proposeMerge(category, a, b)`. */
	propose?: { a: string; b: string };
}

export function stepMerge(state: MergeState, action: MergeAction, ctx: MergeContext): MergeStep {
	switch (action.type) {
		case 'toggle':
			return { state: state.step === 'idle' ? { step: 'first' } : MERGE_IDLE };
		case 'cancel':
			return { state: MERGE_IDLE };
		case 'pick': {
			if (state.step === 'first') {
				return firstPickable(ctx).includes(action.player)
					? { state: { step: 'second', first: action.player } }
					: { state };
			}
			if (state.step === 'second') {
				if (action.player === state.first) return { state: { step: 'first' } };
				return pairBlocker(ctx, state.first, action.player) === null
					? { state: MERGE_IDLE, propose: { a: state.first, b: action.player } }
					: { state };
			}
			return { state };
		}
	}
}
