import type { Cell } from '$lib/protocol';

export type BadgeKind =
	'unique' | 'shared' | 'zero' | 'wrong_letter' | 'rejected' | 'empty' | 'pending';
export type BadgeTone = 'lime' | 'banana' | 'grey';

export interface CellBadge {
	kind: BadgeKind;
	/** Points of the cell (0 when it is not valid). */
	points: number;
	/** Colour of the points pill, `null` when no pill is shown. */
	tone: BadgeTone | null;
	/** The answer is crossed out. */
	struck: boolean;
	/** Reason chip shown next to the answer. */
	chip: 'wrong_letter' | 'rejected' | null;
}

/**
 * Classifies a scored cell for the points pill: 10 unique = lime, 5 shared =
 * banana, 0 = grey, empty answers have no pill. `undefined` (no result yet)
 * shows nothing.
 */
export function classifyCell(cell: Cell | undefined): CellBadge {
	if (!cell) return { kind: 'pending', points: 0, tone: null, struck: false, chip: null };
	switch (cell.status) {
		case 'empty':
			return { kind: 'empty', points: 0, tone: null, struck: false, chip: null };
		case 'wrong_letter':
			return { kind: 'wrong_letter', points: 0, tone: 'grey', struck: false, chip: 'wrong_letter' };
		case 'rejected':
			return { kind: 'rejected', points: 0, tone: 'grey', struck: true, chip: 'rejected' };
		case 'valid':
			if (cell.points >= 10) {
				return { kind: 'unique', points: cell.points, tone: 'lime', struck: false, chip: null };
			}
			if (cell.points > 0) {
				return { kind: 'shared', points: cell.points, tone: 'banana', struck: false, chip: null };
			}
			return { kind: 'zero', points: 0, tone: 'grey', struck: false, chip: null };
	}
}
