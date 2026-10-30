// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type Api } from '$lib/api';
import type { PublicRoomsResponse } from '$lib/protocol';
import { RoomBrowser } from './browser.svelte';

const room = {
	code: 'KT7P',
	lang: 'en' as const,
	players: 3,
	maxPlayers: 10,
	categoryLabels: ['Name'],
	customCategories: 0,
	rounds: 5,
	timeLimitSeconds: 60
};
function deferred() {
	let resolve!: (value: PublicRoomsResponse) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<PublicRoomsResponse>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}
function visible(value: 'visible' | 'hidden') {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value });
	document.dispatchEvent(new Event('visibilitychange'));
}
let stop: () => void;
beforeEach(() => {
	vi.useFakeTimers();
	visible('visible');
});
afterEach(() => {
	stop?.();
	vi.useRealTimers();
});
function setup(listRooms = vi.fn<Api['listRooms']>().mockResolvedValue({ rooms: [room] })) {
	const browser = new RoomBrowser('en', { listRooms });
	stop = browser.start(document);
	return { browser, listRooms };
}

describe('room discovery lifecycle', () => {
	it('follows the paused flag of the server: set while public games are off, cleared when they are back', async () => {
		const listRooms = vi
			.fn<Api['listRooms']>()
			.mockResolvedValueOnce({ rooms: [], paused: true })
			.mockResolvedValue({ rooms: [room] });
		const { browser } = setup(listRooms);
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.paused).toBe(true);
		expect(browser.rooms).toEqual([]);
		await vi.advanceTimersByTimeAsync(15_000);
		expect(browser.paused).toBe(false);
		expect(browser.rooms).toEqual([room]);
	});
	it('polls every 15 seconds only while visible, refreshes on return and cleans up', async () => {
		const { browser, listRooms } = setup();
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.rooms).toEqual([room]);
		await vi.advanceTimersByTimeAsync(15_000);
		expect(listRooms).toHaveBeenCalledTimes(2);
		visible('hidden');
		await vi.advanceTimersByTimeAsync(60_000);
		expect(listRooms).toHaveBeenCalledTimes(2);
		visible('visible');
		await vi.advanceTimersByTimeAsync(0);
		expect(listRooms).toHaveBeenCalledTimes(3);
		stop();
		visible('visible');
		await vi.advanceTimersByTimeAsync(60_000);
		expect(listRooms).toHaveBeenCalledTimes(3);
	});
	it('never overlaps active fetches and aborts on hide', async () => {
		const pending = deferred();
		const { browser, listRooms } = setup(
			vi.fn<Api['listRooms']>().mockReturnValue(pending.promise)
		);
		await browser.refresh();
		await vi.advanceTimersByTimeAsync(30_000);
		expect(listRooms).toHaveBeenCalledOnce();
		const signal = listRooms.mock.calls[0][1]!.signal!;
		visible('hidden');
		expect(signal.aborted).toBe(true);
		pending.resolve({ rooms: [room] });
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.rooms).toEqual([]);
	});
	it.each(['success', 'failure'])('ignores stale %s after changing the filter', async (result) => {
		const old = deferred();
		const listRooms = vi
			.fn<Api['listRooms']>()
			.mockReturnValueOnce(old.promise)
			.mockResolvedValue({ rooms: [{ ...room, lang: 'nl' }] });
		const { browser } = setup(listRooms);
		browser.filter('nl');
		await vi.advanceTimersByTimeAsync(0);
		if (result === 'success') old.resolve({ rooms: [room] });
		else old.reject(new ApiError(503, 'server_busy', 300));
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.rooms[0].lang).toBe('nl');
		expect(browser.error).toBeNull();
		expect(listRooms.mock.calls[0][1]!.signal!.aborted).toBe(true);
	});
	it('honours Retry-After for manual, filter and visibility refreshes and recovers', async () => {
		const listRooms = vi
			.fn<Api['listRooms']>()
			.mockRejectedValueOnce(new ApiError(429, 'rate_limited', 60))
			.mockResolvedValue({ rooms: [room] });
		const { browser } = setup(listRooms);
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.error).toBe('rate_limited');
		await browser.refresh();
		browser.filter('es');
		visible('hidden');
		visible('visible');
		await vi.advanceTimersByTimeAsync(59_999);
		expect(listRooms).toHaveBeenCalledOnce();
		await vi.advanceTimersByTimeAsync(1);
		expect(listRooms).toHaveBeenCalledTimes(2);
		expect(browser.error).toBeNull();
	});
	it('bounds the list and keeps last successful results on a polling error', async () => {
		const listRooms = vi
			.fn<Api['listRooms']>()
			.mockResolvedValueOnce({ rooms: Array.from({ length: 60 }, () => room) })
			.mockRejectedValue(new ApiError(0, 'network'));
		const { browser } = setup(listRooms);
		await vi.advanceTimersByTimeAsync(0);
		expect(browser.rooms).toHaveLength(50);
		await vi.advanceTimersByTimeAsync(15_000);
		expect(browser.rooms).toHaveLength(50);
		expect(browser.error).toBe('network');
	});
});
