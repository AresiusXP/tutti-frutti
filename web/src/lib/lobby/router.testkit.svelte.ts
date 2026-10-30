/**
 * A stand-in for SvelteKit's router, for tests of code that uses shallow routing: `page.state`
 * (reactive) plus a tiny browser history that `pushState` and `history.back()` move through.
 * Wire it with `vi.mock('$app/navigation', ...)`, `vi.mock('$app/state', ...)` and
 * `vi.spyOn(history, 'back').mockImplementation(fakeRouter.back)`.
 */
type PageState = { tfSheet?: number };

export const page = $state<{ state: PageState }>({ state: {} });

let entries: PageState[] = [{}];
let index = 0;

export const fakeRouter = {
	reset() {
		entries = [{}];
		index = 0;
		page.state = {};
	},
	pushState(_url: string, state: PageState) {
		entries = [...entries.slice(0, index + 1), state];
		index += 1;
		page.state = state;
	},
	/** Back / `history.back()`: SvelteKit sets `page.state` of the entry we land on. */
	back() {
		if (index === 0) return;
		index -= 1;
		page.state = entries[index];
	},
	/** A real navigation (`goto`, a link): a new entry whose state is empty. */
	navigate() {
		this.pushState('/elsewhere', {});
	},
	/** How many entries sit above the first one: what the sheets leave behind. */
	get depth() {
		return index;
	}
};
