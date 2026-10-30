import { pushState } from '$app/navigation';
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

/** `null` without a router (unit tests, static render): the sheet then simply has no Back handling. */
export function claimBack(): BackClaim | null {
	let depth: number;
	try {
		depth = depthNow() + 1;
		pushState('', { [KEY]: depth });
	} catch {
		return null;
	}
	return {
		get gone() {
			return depthNow() < depth;
		},
		release() {
			if (depthNow() !== depth) return;
			expectPop();
			history.back();
		}
	};
}
