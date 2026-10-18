import { describe, expect, it, vi } from 'vitest';
import {
	createWakeLock,
	type WakeLockDocument,
	type WakeLockNavigator,
	type WakeLockSentinelLike
} from './wakelock';

class FakeSentinel implements WakeLockSentinelLike {
	released = false;
	#listeners: (() => void)[] = [];
	release = vi.fn(async () => {
		this.released = true;
	});
	addEventListener(_type: 'release', listener: () => void) {
		this.#listeners.push(listener);
	}
	/** The browser let go of the lock (e.g. the tab was hidden). */
	revoke() {
		this.released = true;
		for (const l of this.#listeners) l();
	}
}

class FakeDocument implements WakeLockDocument {
	visibilityState = 'visible';
	#listeners = new Set<() => void>();
	addEventListener(_type: 'visibilitychange', listener: () => void) {
		this.#listeners.add(listener);
	}
	removeEventListener(_type: 'visibilitychange', listener: () => void) {
		this.#listeners.delete(listener);
	}
	get listenerCount() {
		return this.#listeners.size;
	}
	setVisibility(state: 'visible' | 'hidden') {
		this.visibilityState = state;
		for (const l of [...this.#listeners]) l();
	}
}

function setup() {
	const sentinels: FakeSentinel[] = [];
	const request = vi.fn(async (_type: 'screen') => {
		const s = new FakeSentinel();
		sentinels.push(s);
		return s;
	});
	const nav: WakeLockNavigator = { wakeLock: { request } };
	const doc = new FakeDocument();
	return { nav, doc, request, sentinels, lock: createWakeLock(nav, doc) };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('createWakeLock', () => {
	it('requests a screen lock once, even if acquired twice', async () => {
		const { lock, request } = setup();
		lock.acquire();
		lock.acquire();
		await settle();
		expect(request).toHaveBeenCalledTimes(1);
		expect(request).toHaveBeenCalledWith('screen');
	});

	it('releases the lock and the listener on release', async () => {
		const { lock, doc, sentinels } = setup();
		lock.acquire();
		await settle();
		expect(doc.listenerCount).toBe(1);
		lock.release();
		expect(sentinels[0].release).toHaveBeenCalledTimes(1);
		expect(doc.listenerCount).toBe(0);
		lock.release();
		expect(sentinels[0].release).toHaveBeenCalledTimes(1);
	});

	it('acquires again when the page becomes visible after the browser revoked the lock', async () => {
		const { lock, doc, request, sentinels } = setup();
		lock.acquire();
		await settle();
		doc.setVisibility('hidden');
		sentinels[0].revoke();
		await settle();
		expect(request).toHaveBeenCalledTimes(1);
		doc.setVisibility('visible');
		await settle();
		expect(request).toHaveBeenCalledTimes(2);
		expect(sentinels).toHaveLength(2);
	});

	it('does not request a second lock while one is held', async () => {
		const { lock, doc, request } = setup();
		lock.acquire();
		await settle();
		doc.setVisibility('visible');
		await settle();
		expect(request).toHaveBeenCalledTimes(1);
	});

	it('does not request while the page is hidden, but does when it shows', async () => {
		const { lock, doc, request } = setup();
		doc.visibilityState = 'hidden';
		lock.acquire();
		await settle();
		expect(request).not.toHaveBeenCalled();
		doc.setVisibility('visible');
		await settle();
		expect(request).toHaveBeenCalledTimes(1);
	});

	it('is silent when the API is missing', async () => {
		const doc = new FakeDocument();
		const lock = createWakeLock({}, doc);
		expect(() => lock.acquire()).not.toThrow();
		await settle();
		expect(() => lock.release()).not.toThrow();
		const none = createWakeLock(undefined, undefined);
		expect(() => none.acquire()).not.toThrow();
		expect(() => none.release()).not.toThrow();
	});

	it('is silent when the request is rejected and retries on the next visibility change', async () => {
		const request = vi.fn().mockRejectedValueOnce(new Error('NotAllowedError'));
		const doc = new FakeDocument();
		const sentinel = new FakeSentinel();
		request.mockResolvedValue(sentinel);
		const lock = createWakeLock({ wakeLock: { request } }, doc);
		expect(() => lock.acquire()).not.toThrow();
		await settle();
		expect(request).toHaveBeenCalledTimes(1);
		doc.setVisibility('visible');
		await settle();
		expect(request).toHaveBeenCalledTimes(2);
		lock.release();
		expect(sentinel.release).toHaveBeenCalled();
	});

	it('releases a lock that was granted after release() was called', async () => {
		const { lock, sentinels } = setup();
		lock.acquire();
		lock.release();
		await settle();
		expect(sentinels).toHaveLength(1);
		expect(sentinels[0].release).toHaveBeenCalledTimes(1);
	});

	it('survives a sentinel whose release throws', async () => {
		const sentinel = new FakeSentinel();
		sentinel.release = vi.fn(() => {
			throw new Error('boom');
		});
		const lock = createWakeLock(
			{ wakeLock: { request: async () => sentinel } },
			new FakeDocument()
		);
		lock.acquire();
		await settle();
		expect(() => lock.release()).not.toThrow();
	});
});
