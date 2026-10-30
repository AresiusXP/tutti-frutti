/**
 * The open sheets, oldest first. A sheet can open another one while it stays mounted (the legal
 * sheet opens the list of blocked players), and every sheet listens to the window's keyboard: only
 * the topmost one may react to Escape and Tab, otherwise one Escape would close the whole stack and
 * two focus traps would fight over the focus.
 */
const open: symbol[] = [];

/** Registers a newly opened sheet and returns its token; call `leaveStack` with it when it closes. */
export function enterStack(): symbol {
	const token = Symbol('sheet');
	open.push(token);
	return token;
}

export function leaveStack(token: symbol): void {
	const index = open.indexOf(token);
	if (index >= 0) open.splice(index, 1);
}

/** Whether `token` is the sheet on top (the only one the keyboard belongs to). */
export function isTopSheet(token: symbol | undefined): boolean {
	return token !== undefined && open[open.length - 1] === token;
}
