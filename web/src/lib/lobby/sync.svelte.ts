import { onDestroy, untrack } from 'svelte';
import type { RoomSession } from '$lib/game/session';
import type { Settings } from '$lib/protocol';
import { sameSettings } from './settings';

/** How long an unconfirmed edit is shown before the screen goes back to the server's value. */
export const PENDING_MS = 3000;

/**
 * Keeps the host's edits responsive without trusting them: every change is
 * built from the CURRENT settings (the server's, or our last unconfirmed edit)
 * and sent whole. The edit is shown right away (`pending`); it is dropped as
 * soon as the server's settings equal it, the server refuses it (`error`), or
 * `PENDING_MS` passes, so the screen always converges to `view.settings`.
 * Call it during component initialisation.
 */
export function createSettingsSync(getSession: () => RoomSession) {
	let pending = $state.raw<Settings | null>(null);
	let failed = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;
	let seenError = untrack(() => getSession().lastError?.seq ?? 0);

	const server = $derived(getSession().view?.settings ?? null);

	function clearPending() {
		pending = null;
		clearTimeout(timer);
		timer = undefined;
	}

	$effect(() => {
		if (pending && server && sameSettings(server, pending)) clearPending();
	});

	$effect(() => {
		const error = getSession().lastError;
		if (!error || error.seq <= seenError) return;
		seenError = error.seq;
		if (error.ref === 'update_settings') {
			clearPending();
			failed = true;
		}
	});

	onDestroy(() => clearTimeout(timer));

	return {
		/** What to show: our unconfirmed edit, else the server's settings. */
		get settings(): Settings | null {
			return pending ?? server;
		},
		/** True while an edit has not been confirmed yet. */
		get saving(): boolean {
			return pending !== null;
		},
		/** The server refused the last edit (calm inline message; cleared by the next edit). */
		get failed(): boolean {
			return failed;
		},
		/** Sends `next` (the whole settings object) unless it equals what is shown. */
		commit(next: Settings): void {
			const current = pending ?? server;
			if (current && sameSettings(next, current)) return;
			failed = false;
			pending = next;
			getSession().updateSettings(next);
			clearTimeout(timer);
			timer = setTimeout(clearPending, PENDING_MS);
		}
	};
}
