// Waiting for the pop of a closed sheet's history entry (no router import: usable from plain stores).
/**
 * `history.back()` is asynchronous: the router handles the `popstate` that follows as a navigation of its
 * own, which CANCELS a `goto()` started in between (a sheet that closes and then sends the player to a
 * room would leave them where they were). Code that navigates right after closing a sheet awaits
 * `historySettled()` first: it resolves once the pop of the sheet's entry has been seen (or after a short
 * timeout, or at once when nothing is popping).
 */
let popping: Promise<void> | null = null;
const POP_TIMEOUT_MS = 400;

export function expectPop(): void {
	if (typeof window === 'undefined') return;
	const pending = new Promise<void>((resolve) => {
		const done = () => {
			window.removeEventListener('popstate', done);
			clearTimeout(timer);
			// the router's own listener already ran: let its (synchronous part of the) navigation start first
			setTimeout(() => {
				if (popping === pending) popping = null;
				resolve();
			}, 0);
		};
		const timer = setTimeout(done, POP_TIMEOUT_MS);
		window.addEventListener('popstate', done);
	});
	popping = pending;
}

export function historySettled(): Promise<void> {
	return popping ?? Promise.resolve();
}
