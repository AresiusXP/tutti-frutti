import { goto } from '$app/navigation';
import { page } from '$app/state';
import { expectPop } from './historyPop';

/**
 * Android Back for modal sheets. Opening a sheet pushes one history entry (same URL, SvelteKit
 * shallow routing: `page.state.tfSheet` = how many sheets are open); the browser's Back, which
 * is also the Android system Back, then pops that entry and the sheet closes instead of leaving
 * the room. A sheet closed by other means (button, Escape, backdrop) pops its own entry again,
 * so the history does not grow. Nothing here talks to the game: no `leave` is ever sent.
 *
 * The on-screen keyboard is not our business: Android dismisses it with the first Back press and
 * only the next one reaches the page.
 */
const KEY = 'tfSheet';

const depthNow = () => page.state[KEY] ?? 0;

export interface BackClaim {
	/** Reactive: the entry was popped (Back, forward navigation) or a navigation replaced the state. */
	readonly gone: boolean;
	/** Pops the entry when it is still the newest one (the sheet was closed by other means). */
	release(): void;
}

/** Without a router (unit tests, static render) `goto` rejects: the claim then never goes away and never pops, so the sheet simply has no Back handling. */
export function claimBack(): BackClaim | null {
	const depth = depthNow() + 1;
	// `goto` applies the state asynchronously: until the entry is seen, nothing is "gone" (nor poppable)
	let seen = false;
	let retried = false;
	let pushed: Promise<void>;
	try {
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- same URL: only the history state changes
		pushed = goto('', { shallow: true, state: { [KEY]: depth } });
	} catch {
		return null;
	}
	// no router (not started, static render): the sheet has no Back handling, as if there were no claim
	pushed.catch(() => {});
	const observe = () => {
		const now = depthNow();
		if (now >= depth) seen = true;
		return now;
	};
	return {
		get gone() {
			const now = observe();
			return seen && now < depth;
		},
		release() {
			if (observe() !== depth) {
				// closed before the entry landed: pop it as soon as it has
				if (!seen && !retried) {
					retried = true;
					void pushed.then(
						() => this.release(),
						() => {}
					);
				}
				return;
			}
			expectPop();
			history.back();
		}
	};
}
