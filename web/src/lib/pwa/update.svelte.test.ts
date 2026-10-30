// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
	ACTIVATE_WAIT_MS,
	applyUpdate,
	CHECK_INTERVAL_MS,
	checkForUpdate,
	FOREGROUND_CHECK_MS,
	holdReload,
	INSTALL_WAIT_MS,
	RELOAD_GUARD_MS,
	RELOAD_STAMP_KEY,
	resetUpdateForTests,
	startUpdates,
	update
} from './update.svelte';
import {
	FakeRegistration,
	FakeWorker,
	installFakeServiceWorker,
	restoreFakeServiceWorker,
	type FakeContainer
} from './sw.testkit';

type Options = {
	onNeedRefresh?: () => void;
	onNeedReload?: () => void;
	onRegisteredSW?: (url: string, registration: ServiceWorkerRegistration | undefined) => void;
};

const plugin = vi.hoisted(() => ({
	options: undefined as unknown,
	registerSW: vi.fn((_options: unknown) => (_reload?: boolean) => Promise.resolve()),
	updateSW: vi.fn((_reload?: boolean) => Promise.resolve())
}));
vi.mock('virtual:pwa-register', () => ({
	registerSW: (options: unknown) => {
		plugin.options = options;
		return plugin.registerSW(options);
	}
}));

const options = () => plugin.options as Options;
const asRegistration = (r: FakeRegistration | undefined) =>
	r as unknown as ServiceWorkerRegistration;

let container: FakeContainer;
let reload: ReturnType<typeof vi.fn>;
let online = true;

beforeEach(() => {
	resetUpdateForTests();
	sessionStorage.clear();
	online = true;
	vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online);
	({ container, reload } = installFakeServiceWorker());
	plugin.updateSW = vi.fn(() => Promise.resolve());
	plugin.registerSW = vi.fn(() => plugin.updateSW);
	plugin.options = undefined;
});

afterEach(() => {
	vi.useRealTimers();
	restoreFakeServiceWorker();
	vi.restoreAllMocks();
});

/** A worker that waits, and a plugin whose "skip waiting" makes it take over like a real one. */
function waitingWorker() {
	const worker = new FakeWorker('installed');
	container.registration!.waiting = worker;
	plugin.updateSW.mockImplementation(() => {
		queueMicrotask(() => container.dispatchEvent(new Event('controllerchange')));
		return Promise.resolve();
	});
	return worker;
}

describe('startUpdates', () => {
	it('registers the worker once and raises needRefresh when one waits', async () => {
		const stop = startUpdates();
		startUpdates();
		await vi.waitFor(() => expect(plugin.registerSW).toHaveBeenCalledOnce());
		expect(plugin.options).toMatchObject({ immediate: true });
		expect(update.needRefresh).toBe(false);
		options().onNeedRefresh?.();
		expect(update.needRefresh).toBe(true);
		stop();
	});

	it('does nothing in a browser without service workers', () => {
		Reflect.deleteProperty(navigator, 'serviceWorker');
		startUpdates()();
		expect(plugin.registerSW).not.toHaveBeenCalled();
	});

	describe('automatic checks', () => {
		let stop: () => void;
		beforeEach(async () => {
			vi.useFakeTimers({
				toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval', 'Date']
			});
			vi.setSystemTime(new Date('2026-10-30T10:00:00Z'));
			stop = startUpdates();
			await vi.waitFor(() => expect(plugin.registerSW).toHaveBeenCalled());
			options().onRegisteredSW?.('/sw.js', asRegistration(container.registration));
		});
		afterEach(() => stop());

		const visible = (state: DocumentVisibilityState) => {
			Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
			document.dispatchEvent(new Event('visibilitychange'));
		};
		const registration = () => container.registration!;

		it('checks every hour', async () => {
			await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
			expect(registration().update).toHaveBeenCalledOnce();
			await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
			expect(registration().update).toHaveBeenCalledTimes(2);
		});

		it('checks when the page comes back or goes online, at most every 15 minutes', async () => {
			visible('visible');
			window.dispatchEvent(new Event('online'));
			await vi.advanceTimersByTimeAsync(0);
			expect(registration().update, 'just registered: too early').not.toHaveBeenCalled();

			await vi.advanceTimersByTimeAsync(FOREGROUND_CHECK_MS - 1000);
			visible('visible');
			await vi.advanceTimersByTimeAsync(0);
			expect(registration().update, 'still under 15 minutes').not.toHaveBeenCalled();

			await vi.advanceTimersByTimeAsync(1000);
			visible('hidden');
			await vi.advanceTimersByTimeAsync(0);
			expect(
				registration().update,
				'going to the background is not a check'
			).not.toHaveBeenCalled();
			visible('visible');
			await vi.advanceTimersByTimeAsync(0);
			expect(registration().update).toHaveBeenCalledOnce();

			visible('visible');
			window.dispatchEvent(new Event('online'));
			await vi.advanceTimersByTimeAsync(0);
			expect(registration().update, 'throttled again').toHaveBeenCalledOnce();

			await vi.advanceTimersByTimeAsync(FOREGROUND_CHECK_MS);
			window.dispatchEvent(new Event('online'));
			await vi.advanceTimersByTimeAsync(0);
			expect(registration().update).toHaveBeenCalledTimes(2);
		});
	});
});

describe('checkForUpdate', () => {
	it('asks the worker script and clears nothing it should not', async () => {
		await checkForUpdate();
		expect(container.registration!.update).toHaveBeenCalledOnce();
		expect(update).toMatchObject({ checking: false, error: null, needRefresh: false });
	});

	it('shares one check between concurrent callers', async () => {
		await Promise.all([checkForUpdate(), checkForUpdate()]);
		expect(container.registration!.update).toHaveBeenCalledOnce();
	});

	it('raises needRefresh when a worker already waits', async () => {
		container.registration!.waiting = new FakeWorker('installed');
		await checkForUpdate();
		expect(update.needRefresh).toBe(true);
	});

	it('offline: reports it without throwing and without fetching, and recovers', async () => {
		online = false;
		await expect(checkForUpdate()).resolves.toBeUndefined();
		expect(update.error).toBe('offline');
		expect(container.registration!.update).not.toHaveBeenCalled();
		online = true;
		await checkForUpdate();
		expect(update.error).toBeNull();
	});

	it('a failed check is reported as failed (offline when the device says so)', async () => {
		container.registration!.update.mockRejectedValueOnce(new TypeError('network'));
		await checkForUpdate();
		expect(update.error).toBe('failed');
		container.registration!.update.mockImplementationOnce(() => {
			online = false;
			return Promise.reject(new TypeError('network'));
		});
		await checkForUpdate();
		expect(update.error).toBe('offline');
	});

	it('without a registration there is nothing to check and nothing to report', async () => {
		container.registration = undefined;
		await checkForUpdate();
		expect(update).toMatchObject({ error: null, checking: false });
	});
});

describe('applyUpdate', () => {
	let stop: () => void;
	beforeEach(async () => {
		stop = startUpdates();
		await vi.waitFor(() => expect(plugin.registerSW).toHaveBeenCalled());
	});
	afterEach(() => stop());

	it('lets the waiting worker take over, then reloads once and remembers it', async () => {
		waitingWorker();
		await applyUpdate();
		expect(plugin.updateSW).toHaveBeenCalledWith(true);
		expect(reload).toHaveBeenCalledOnce();
		expect(Number(sessionStorage.getItem(RELOAD_STAMP_KEY))).toBeGreaterThan(0);
		expect(update.applying, 'the button stays off while the page reloads').toBe(true);
	});

	it('the plugin reloading on its own (controlling event) does not reload twice', async () => {
		const worker = waitingWorker();
		plugin.updateSW.mockImplementation(() => {
			options().onNeedReload?.();
			worker.moveTo('activated');
			return Promise.resolve();
		});
		await applyUpdate();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('also finishes when the worker activates without a controller change (uncontrolled page)', async () => {
		const worker = waitingWorker();
		plugin.updateSW.mockImplementation(() => {
			queueMicrotask(() => worker.moveTo('activated'));
			return Promise.resolve();
		});
		await applyUpdate();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('waits for a worker that is still installing', async () => {
		const worker = new FakeWorker('installing');
		container.registration!.installing = worker;
		const done = applyUpdate();
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(reload).not.toHaveBeenCalled();
		container.registration!.installing = null;
		container.registration!.waiting = worker;
		plugin.updateSW.mockImplementation(() => {
			queueMicrotask(() => container.dispatchEvent(new Event('controllerchange')));
			return Promise.resolve();
		});
		worker.moveTo('installed');
		await done;
		expect(reload).toHaveBeenCalledOnce();
	});

	it('no new version after a fresh check: not_ready, no reload (the old shell would come back)', async () => {
		await applyUpdate();
		expect(container.registration!.update).toHaveBeenCalledOnce();
		expect(update.error).toBe('not_ready');
		expect(reload).not.toHaveBeenCalled();
		expect(update.applying).toBe(false);
		await applyUpdate();
		expect(reload, 'still no reload loop').not.toHaveBeenCalled();
	});

	it('a worker found by the check is applied', async () => {
		container.registration!.update.mockImplementation(() => {
			waitingWorker();
			return Promise.resolve();
		});
		await applyUpdate();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('offline: says so and does not claim the version is merely not ready', async () => {
		online = false;
		await applyUpdate();
		expect(update.error).toBe('offline');
		expect(reload).not.toHaveBeenCalled();
	});

	it('a worker that never takes over ends as failed, not as a hang', async () => {
		vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
		container.registration!.waiting = new FakeWorker('installed');
		const done = applyUpdate();
		await vi.advanceTimersByTimeAsync(ACTIVATE_WAIT_MS);
		await done;
		expect(update.error).toBe('failed');
		expect(reload).not.toHaveBeenCalled();
		expect(update.applying).toBe(false);
	});

	it('a worker still installing after a long while ends as not_ready', async () => {
		vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
		container.registration!.installing = new FakeWorker('installing');
		const done = applyUpdate();
		await vi.advanceTimersByTimeAsync(INSTALL_WAIT_MS * 2);
		await done;
		expect(update.error).toBe('not_ready');
		expect(reload).not.toHaveBeenCalled();
	});

	describe('without a service worker', () => {
		it('a browser without workers just reloads', async () => {
			Reflect.deleteProperty(navigator, 'serviceWorker');
			await applyUpdate();
			expect(reload).toHaveBeenCalledOnce();
		});

		it('a page no worker controls and nothing waits: the network has the newest shell, reload', async () => {
			container.controller = null;
			await applyUpdate();
			expect(reload).toHaveBeenCalledOnce();
		});
	});

	describe('loop guard', () => {
		it('does not reload again within 30 s of a reload made for an update', async () => {
			sessionStorage.setItem(RELOAD_STAMP_KEY, String(Date.now() - 5000));
			waitingWorker();
			await applyUpdate();
			expect(reload).not.toHaveBeenCalled();
			expect(update.error).toBe('not_ready');
			expect(update.applying).toBe(false);
		});

		it('reloads again once the window has passed', async () => {
			sessionStorage.setItem(RELOAD_STAMP_KEY, String(Date.now() - RELOAD_GUARD_MS - 1000));
			waitingWorker();
			await applyUpdate();
			expect(reload).toHaveBeenCalledOnce();
		});

		it('keeps the room marker and the URL: only the stamp is written', async () => {
			sessionStorage.setItem('tf.room.KT7P', '1');
			waitingWorker();
			await applyUpdate();
			expect(reload).toHaveBeenCalledOnce();
			expect(sessionStorage.getItem('tf.room.KT7P')).toBe('1');
		});

		it('still reloads when sessionStorage is unavailable', async () => {
			vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
				throw new Error('denied');
			});
			vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
				throw new Error('denied');
			});
			waitingWorker();
			await applyUpdate();
			expect(reload).toHaveBeenCalledOnce();
		});
	});
});

describe('applyUpdate before the plugin registered', () => {
	it('still tells the waiting worker to skip waiting itself', async () => {
		const worker = new FakeWorker('installed');
		container.registration!.waiting = worker;
		const done = applyUpdate();
		await new Promise((resolve) => setTimeout(resolve, 0));
		worker.moveTo('activated');
		await done;
		expect(worker.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' });
		expect(reload).toHaveBeenCalledOnce();
	});
});

describe('another tab accepted the update', () => {
	async function started() {
		startUpdates();
		await vi.waitFor(() => expect(plugin.options).toBeDefined());
	}

	it('reloads this tab when nothing is going on', async () => {
		await started();
		options().onNeedReload?.();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('does NOT reload a tab in the middle of a round: the banner stays, the player decides', async () => {
		await started();
		let playing = true;
		const release = holdReload(() => playing);
		options().onNeedReload?.();
		expect(reload).not.toHaveBeenCalled();
		expect(update.needRefresh).toBe(true);

		// the worker is already the new one: the tap only reloads, it does not wait for anything
		await applyUpdate();
		expect(plugin.updateSW).not.toHaveBeenCalled();
		expect(reload).toHaveBeenCalledOnce();
		release();
		playing = false;
	});

	it('an explicit tap during a round of THIS tab still applies the update', async () => {
		await started();
		holdReload(() => true);
		waitingWorker();
		await applyUpdate();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('a released hold no longer blocks', async () => {
		await started();
		const release = holdReload(() => true);
		release();
		options().onNeedReload?.();
		expect(reload).toHaveBeenCalledOnce();
	});
});
