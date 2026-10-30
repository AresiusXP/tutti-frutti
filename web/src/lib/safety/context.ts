/**
 * What the room screens can ask of the safety layer. `SafetyHost` (mounted by `RoomScreen`) provides it;
 * a component rendered without a host (a unit test of a card, say) gets `null` and simply shows no
 * report affordance.
 */
import { getContext, setContext } from 'svelte';
import type { Person, Shown } from './targets';

/** What the sheet opens on. */
export type SafetyRequest =
	/** The other players, to pick one (the screens that show no player row: letter, playing). */
	| { start: 'players' }
	/** Menu of one person: their answer when shown, their nickname and avatar, block. */
	| { start: 'person'; person: Person; answer?: Shown }
	/** Straight to the report of a custom category label (the label as the viewer sees it). */
	| { start: 'label'; category: number; label: string }
	/** Straight to the report of the AI's note on `person`'s answer (anybody's, the viewer's own included). */
	| { start: 'verdict'; person: Person; verdict: Shown };

export interface SafetyActions {
	/** The viewer's seat id. */
	readonly youId: string;
	/** Not the viewer, not an automated seat: the ones a report or a block may name. */
	canTarget(id: string): boolean;
	open(request: SafetyRequest): void;
}

const KEY = Symbol('tf.safety');

export const provideSafety = (actions: SafetyActions): void => {
	setContext(KEY, actions);
};

export const useSafety = (): SafetyActions | null =>
	getContext<SafetyActions | undefined>(KEY) ?? null;
