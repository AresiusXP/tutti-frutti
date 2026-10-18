/**
 * Keeps the screen on while a round is being played (Screen Wake Lock API).
 * Silent when the API is missing or the request is rejected (battery saver,
 * permissions policy, hidden tab): the game works without it.
 *
 * The browser releases the lock whenever the page is hidden, so it is requested
 * again on `visibilitychange` while it is still wanted.
 */

export interface WakeLockSentinelLike {
	release(): Promise<void>;
	addEventListener?(type: 'release', listener: () => void): void;
}

export interface WakeLockNavigator {
	wakeLock?: { request(type: 'screen'): Promise<WakeLockSentinelLike> };
}

export interface WakeLockDocument {
	visibilityState?: string;
	addEventListener(type: 'visibilitychange', listener: () => void): void;
	removeEventListener(type: 'visibilitychange', listener: () => void): void;
}

export interface ScreenWakeLock {
	/** Starts holding the lock (and keeps re-acquiring it). Calling it twice is harmless. */
	acquire(): void;
	/** Lets go of the lock for good. */
	release(): void;
}

function safeRelease(lock: WakeLockSentinelLike) {
	try {
		void Promise.resolve(lock.release()).catch(() => {});
	} catch {
		// already released
	}
}

export function createWakeLock(
	nav: WakeLockNavigator | undefined = typeof navigator === 'undefined' ? undefined : navigator,
	doc: WakeLockDocument | undefined = typeof document === 'undefined' ? undefined : document
): ScreenWakeLock {
	let wanted = false;
	let pending = false;
	let sentinel: WakeLockSentinelLike | null = null;

	async function request() {
		const api = nav?.wakeLock;
		if (!wanted || pending || sentinel || !api) return;
		if (doc?.visibilityState === 'hidden') return;
		pending = true;
		try {
			const lock = await api.request('screen');
			if (!wanted) {
				safeRelease(lock);
				return;
			}
			sentinel = lock;
			lock.addEventListener?.('release', () => {
				if (sentinel === lock) sentinel = null;
			});
		} catch {
			// unsupported, denied or the page is hidden: keep playing without it
		} finally {
			pending = false;
		}
	}

	const onVisibility = () => {
		if (doc?.visibilityState === 'visible') void request();
	};

	return {
		acquire() {
			if (wanted) return;
			wanted = true;
			doc?.addEventListener('visibilitychange', onVisibility);
			void request();
		},
		release() {
			if (!wanted) return;
			wanted = false;
			doc?.removeEventListener('visibilitychange', onVisibility);
			const held = sentinel;
			sentinel = null;
			if (held) safeRelease(held);
		}
	};
}
