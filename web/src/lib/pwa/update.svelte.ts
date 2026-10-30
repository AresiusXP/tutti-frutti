/**
 * The only owner of the service worker on the page: it registers it, tells the UI that a new
 * version waits (`update.needRefresh`) and is the only code that reloads the page for an update.
 *
 * Why one place: with `registerType: 'prompt'` the new shell only reaches the page after the
 * waiting worker is told to take over. A plain `location.reload()` while it still waits is served
 * the OLD shell by the old worker, which is how the "new version" screen could loop forever.
 */

export type UpdateError =
	/** The device is offline: nothing could be checked. */
	| 'offline'
	/** The check itself failed (server unreachable, worker script not fetchable, took too long). */
	| 'failed'
	/** No new version is ready yet (a fresh check found nothing, or the page just reloaded for one). */
	| 'not_ready';

export interface UpdateState {
	/** A new version is installed and waits (or already took over in another tab). */
	needRefresh: boolean;
	/** An update check is running. */
	checking: boolean;
	/** `applyUpdate()` is running (or the page is about to reload). */
	applying: boolean;
	error: UpdateError | null;
}

export const update = $state<UpdateState>({
	needRefresh: false,
	checking: false,
	applying: false,
	error: null
});

/** Periodic check while the page stays open. */
export const CHECK_INTERVAL_MS = 60 * 60 * 1000;
/** Minimum time between the automatic checks of a page that returns to the foreground / goes online. */
export const FOREGROUND_CHECK_MS = 15 * 60 * 1000;
/** A page that reloaded for an update does not do it again within this window (loop guard). */
export const RELOAD_GUARD_MS = 30 * 1000;
/** How long a freshly found worker may take to install before the update is called "not ready". */
export const INSTALL_WAIT_MS = 15 * 1000;
/** How long the worker may take to take over once told to skip waiting. */
export const ACTIVATE_WAIT_MS = 10 * 1000;
/** sessionStorage key of the loop guard: the time of the last reload made for an update. */
export const RELOAD_STAMP_KEY = 'tf.update.reloadedAt';

let started = false;
let registration: ServiceWorkerRegistration | undefined;
let updateServiceWorker: ((reloadPage?: boolean) => Promise<void>) | undefined;
let lastCheckAt = 0;
let checkInFlight: Promise<void> | null = null;
/** The new worker already controls this page (another tab accepted the update): only a reload is left. */
let controllerChanged = false;
let reloading = false;
let holds: (() => boolean)[] = [];

const offline = () => typeof navigator !== 'undefined' && navigator.onLine === false;

/**
 * Registers a predicate that says "reloading now would hurt" (a round in progress). While any
 * returns true a tab does NOT reload by itself when ANOTHER tab accepted the update; the banner
 * stays and the player decides. An explicit tap on Reload always goes through. Returns the release.
 */
export function holdReload(busy: () => boolean): () => void {
	holds = [...holds, busy];
	return () => void (holds = holds.filter((hold) => hold !== busy));
}

async function findRegistration(): Promise<ServiceWorkerRegistration | undefined> {
	if (registration) return registration;
	try {
		return await navigator.serviceWorker?.getRegistration();
	} catch {
		return undefined;
	}
}

function markWaiting() {
	update.needRefresh = true;
	if (update.error === 'not_ready') update.error = null;
}

/**
 * Registers the worker (once per page) and keeps looking for updates: hourly, and when the page
 * comes back to the foreground or the device goes online, at most every 15 minutes. Returns the
 * cleanup (tests, HMR). Does nothing without service worker support.
 */
export function startUpdates(): () => void {
	if (started || typeof window === 'undefined' || !('serviceWorker' in navigator)) {
		return () => {};
	}
	started = true;
	let stopped = false;

	// `virtual:pwa-register` touches `window`: import it lazily, on the client only.
	void import('virtual:pwa-register').then(({ registerSW }) => {
		if (stopped) return;
		updateServiceWorker = registerSW({
			immediate: true,
			onNeedRefresh: markWaiting,
			// Replaces the plugin's unconditional reload: it fires in EVERY open tab that saw the
			// update once any tab let the new worker take over.
			onNeedReload: onControllerChange,
			onRegisteredSW(_url, reg) {
				registration = reg;
				lastCheckAt = Date.now();
			},
			onRegisterError() {
				// a page without a worker keeps working: nothing to update
			}
		});
	});

	const timer = setInterval(() => void checkForUpdate(), CHECK_INTERVAL_MS);
	const foreground = () => {
		if (document.visibilityState === 'hidden') return;
		if (Date.now() - lastCheckAt < FOREGROUND_CHECK_MS) return;
		void checkForUpdate();
	};
	document.addEventListener('visibilitychange', foreground);
	window.addEventListener('online', foreground);

	return () => {
		stopped = true;
		started = false;
		clearInterval(timer);
		document.removeEventListener('visibilitychange', foreground);
		window.removeEventListener('online', foreground);
	};
}

/**
 * Asks the server for a newer worker script. Never throws: offline and failures end up in
 * `update.error`. Concurrent calls share one check. A newer worker installs in the background and
 * then raises `update.needRefresh`.
 */
export function checkForUpdate(): Promise<void> {
	checkInFlight ??= runCheck().finally(() => {
		checkInFlight = null;
	});
	return checkInFlight;
}

async function runCheck(): Promise<void> {
	const reg = await findRegistration();
	if (!reg) return;
	if (offline()) {
		update.error = 'offline';
		return;
	}
	update.checking = true;
	lastCheckAt = Date.now();
	try {
		await reg.update();
		if (update.error === 'offline' || update.error === 'failed') update.error = null;
		if (reg.waiting) markWaiting();
	} catch {
		update.error = offline() ? 'offline' : 'failed';
	} finally {
		update.checking = false;
	}
}

/** The waiting worker, giving one that is still installing time to finish. */
async function waitingWorker(reg: ServiceWorkerRegistration): Promise<ServiceWorker | null> {
	if (reg.waiting) return reg.waiting;
	const installing = reg.installing;
	if (!installing) return null;
	await new Promise<void>((resolve) => {
		const finish = () => {
			clearTimeout(timer);
			installing.removeEventListener('statechange', onState);
			resolve();
		};
		const onState = () => {
			if (installing.state !== 'parsed' && installing.state !== 'installing') finish();
		};
		const timer = setTimeout(finish, INSTALL_WAIT_MS);
		installing.addEventListener('statechange', onState);
		onState();
	});
	return reg.waiting;
}

/** Tells the waiting worker to take over and resolves once it did (or fails after a while). */
function takeOver(waiting: ServiceWorker): Promise<void> {
	return new Promise<void>((resolve, reject) => {
		const container = navigator.serviceWorker;
		const finish = (error?: Error) => {
			clearTimeout(timer);
			container.removeEventListener('controllerchange', onChange);
			waiting.removeEventListener('statechange', onState);
			if (error) reject(error);
			else resolve();
		};
		const onChange = () => finish();
		const onState = () => {
			if (waiting.state === 'activated') finish();
			else if (waiting.state === 'redundant') finish(new Error('the new worker became redundant'));
		};
		const timer = setTimeout(
			() => finish(new Error('the new worker did not take over')),
			ACTIVATE_WAIT_MS
		);
		container.addEventListener('controllerchange', onChange);
		waiting.addEventListener('statechange', onState);
		if (updateServiceWorker) updateServiceWorker(true).catch(finish);
		else waiting.postMessage({ type: 'SKIP_WAITING' });
	});
}

function lastReloadAt(): number {
	try {
		return Number(sessionStorage.getItem(RELOAD_STAMP_KEY)) || 0;
	} catch {
		return 0;
	}
}

/**
 * The one place that reloads for an update. The URL (and with it the room) is kept, and so is the
 * per-tab room marker in sessionStorage. A page that already reloaded for an update less than
 * 30 s ago does not reload again: it reports `not_ready` instead of looping.
 */
function reloadForUpdate(): void {
	if (reloading) return;
	const now = Date.now();
	if (now - lastReloadAt() < RELOAD_GUARD_MS) {
		update.error = 'not_ready';
		return;
	}
	try {
		sessionStorage.setItem(RELOAD_STAMP_KEY, String(now));
	} catch {
		// private mode / full storage: the guard is off, the reload still happens
	}
	reloading = true;
	location.reload();
}

function onControllerChange() {
	controllerChanged = true;
	if (update.applying || !holds.some((busy) => busy())) reloadForUpdate();
	else update.needRefresh = true;
}

/**
 * Switches to the newest version and reloads. With a waiting worker: tells it to take over, waits
 * for the switch, reloads. Without one: a fresh check (a worker that is still installing is
 * awaited) and, if there still is none, `not_ready` instead of a reload that would bring the old
 * shell back. A page no worker controls (or a browser without workers) just reloads: the network
 * answers with the newest shell.
 */
export async function applyUpdate(): Promise<void> {
	if (update.applying || reloading) return;
	update.applying = true;
	update.error = null;
	try {
		if (controllerChanged) return reloadForUpdate();
		const reg = await findRegistration();
		if (!reg) return reloadForUpdate();
		let waiting = await waitingWorker(reg);
		if (!waiting) {
			await checkForUpdate();
			if (update.error) return;
			waiting = await waitingWorker(reg);
		}
		if (!waiting) {
			if (navigator.serviceWorker.controller) update.error = 'not_ready';
			else reloadForUpdate();
			return;
		}
		await takeOver(waiting);
		reloadForUpdate();
	} catch {
		update.error = offline() ? 'offline' : 'failed';
	} finally {
		update.applying = reloading;
	}
}

/** Back to the initial state. Tests only. */
export function resetUpdateForTests(): void {
	started = false;
	registration = undefined;
	updateServiceWorker = undefined;
	lastCheckAt = 0;
	checkInFlight = null;
	controllerChanged = false;
	reloading = false;
	holds = [];
	Object.assign(update, { needRefresh: false, checking: false, applying: false, error: null });
}
