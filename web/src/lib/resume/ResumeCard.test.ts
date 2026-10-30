// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type RoomInfo } from '$lib/api';
import { i18n } from '$lib/i18n.svelte';
import { RECENT_ROOM_KEY, RECENT_ROOM_TTL_MS, rememberRecentRoom } from './recent';
import ResumeCard from './ResumeCard.svelte';

const ID = '01020304-0506-4708-890a-0b0c0d0e0f10';
const OTHER_ID = '11121314-1516-4718-991a-1b1c1d1e1f20';

const info = (over: Partial<RoomInfo> = {}): RoomInfo => ({
	code: 'KT7P',
	phase: 'lobby',
	players: 2,
	maxPlayers: 10,
	lang: 'es',
	joinable: true,
	visibility: 'private',
	roomInstanceId: ID,
	...over
});

const stored = () => localStorage.getItem(RECENT_ROOM_KEY);
const button = () => screen.queryByRole('button', { name: /^Retomar la partida reciente/ });

function deferred<T>() {
	let resolve!: (v: T) => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

describe('ResumeCard', () => {
	beforeEach(() => {
		i18n.set('es');
		localStorage.clear();
	});
	afterEach(() => {
		cleanup();
		vi.useRealTimers();
		localStorage.clear();
	});

	it('shows nothing, and asks nobody, when there is no recent room', async () => {
		const load = vi.fn();
		render(ResumeCard, { onresume: vi.fn(), load });
		await Promise.resolve();
		expect(button()).toBeNull();
		expect(load).not.toHaveBeenCalled();
	});

	it('shows nothing while the room is being checked, then a 48 px button with the code only', async () => {
		rememberRecentRoom('KT7P', ID);
		const pending = deferred<RoomInfo>();
		const load = vi.fn(() => pending.promise);
		const { container } = render(ResumeCard, { onresume: vi.fn(), load });
		expect(load).toHaveBeenCalledWith('KT7P', expect.any(AbortSignal));
		expect(button()).toBeNull();
		pending.resolve(info());
		const resume = await screen.findByRole('button', { name: 'Retomar la partida reciente KT7P' });
		expect(resume.className).toContain('min-h-12'); // at least 48 px, and it grows with the text size
		expect(resume.textContent).toContain('KT7P');
		// the card says the code and nothing about who is in the room
		expect(container.textContent?.replace(/\s+/g, ' ').trim()).toBe(
			'↩ Retomar la partida reciente KT7P'
		);
	});

	it.each([
		['en', 'Resume recent game KT7P'],
		['nl', 'Recent spel hervatten KT7P']
	] as const)('is named in %s', async (locale, name) => {
		i18n.set(locale);
		rememberRecentRoom('KT7P', ID);
		render(ResumeCard, { onresume: vi.fn(), load: () => Promise.resolve(info()) });
		expect(await screen.findByRole('button', { name })).toBeTruthy();
	});

	it('tapping it asks the page to resume that room, once the tap happens and not before', async () => {
		rememberRecentRoom('KT7P', ID);
		const onresume = vi.fn();
		render(ResumeCard, { onresume, load: () => Promise.resolve(info()) });
		await screen.findByRole('button', { name: /KT7P/ });
		expect(onresume).not.toHaveBeenCalled();
		await fireEvent.click(screen.getByRole('button', { name: /KT7P/ }));
		expect(onresume).toHaveBeenCalledExactlyOnceWith('KT7P');
		expect(stored()).not.toBeNull(); // forgetting is the room route's job, when the room really ends
	});

	describe('the room is gone or is not the same room', () => {
		it('404: the card never appears and the entry is forgotten', async () => {
			rememberRecentRoom('KT7P', ID);
			const load = vi.fn(() => Promise.reject(new ApiError(404, 'room_not_found')));
			render(ResumeCard, { onresume: vi.fn(), load });
			await waitFor(() => expect(stored()).toBeNull());
			expect(button()).toBeNull();
		});

		it('another instance id (the code was reused): hidden and forgotten', async () => {
			rememberRecentRoom('KT7P', ID);
			render(ResumeCard, {
				onresume: vi.fn(),
				load: () => Promise.resolve(info({ roomInstanceId: OTHER_ID }))
			});
			await waitFor(() => expect(stored()).toBeNull());
			expect(button()).toBeNull();
		});

		it('join_unavailable (a block or restriction): hidden and forgotten', async () => {
			rememberRecentRoom('KT7P', ID);
			render(ResumeCard, {
				onresume: vi.fn(),
				load: () => Promise.reject(new ApiError(403, 'join_unavailable'))
			});
			await waitFor(() => expect(stored()).toBeNull());
			expect(button()).toBeNull();
		});

		it('does not erase an entry another tab wrote while the check was running', async () => {
			rememberRecentRoom('KT7P', ID);
			const pending = deferred<RoomInfo>();
			render(ResumeCard, { onresume: vi.fn(), load: () => pending.promise });
			rememberRecentRoom('BCDF', OTHER_ID); // another tab joined a different room
			pending.reject(new ApiError(404, 'room_not_found'));
			await Promise.resolve();
			await Promise.resolve();
			expect(JSON.parse(stored()!)).toMatchObject({ code: 'BCDF', roomInstanceId: OTHER_ID });
		});
	});

	describe('a room that is not joinable still offers a way back for a player who is seated', () => {
		it.each([
			['full', info({ players: 10, joinable: false })],
			['finished', info({ phase: 'final', joinable: false })],
			['in progress', info({ phase: 'playing', players: 4, joinable: false })]
		])('%s', async (_name, room) => {
			rememberRecentRoom('KT7P', ID);
			const onresume = vi.fn();
			render(ResumeCard, { onresume, load: () => Promise.resolve(room) });
			await fireEvent.click(await screen.findByRole('button', { name: /KT7P/ }));
			expect(onresume).toHaveBeenCalledWith('KT7P'); // the server decides at the join
			expect(stored()).not.toBeNull();
		});
	});

	describe('the check itself fails: not fatal, the button is still honest and usable', () => {
		it.each([
			['offline', new ApiError(0, 'network')],
			['busy', new ApiError(503, 'server_busy')],
			['rate limited', new ApiError(429, 'rate_limited')]
		])('%s', async (_name, error) => {
			rememberRecentRoom('KT7P', ID);
			const onresume = vi.fn();
			render(ResumeCard, { onresume, load: () => Promise.reject(error) });
			await fireEvent.click(await screen.findByRole('button', { name: /KT7P/ }));
			expect(onresume).toHaveBeenCalledWith('KT7P');
			expect(stored()).not.toBeNull();
		});

		it('a check that never answers is given up after 5 s and aborted', async () => {
			vi.useFakeTimers();
			rememberRecentRoom('KT7P', ID);
			let signal: AbortSignal | undefined;
			render(ResumeCard, {
				onresume: vi.fn(),
				load: (_code: string, s: AbortSignal) => {
					signal = s;
					return new Promise<RoomInfo>(() => undefined);
				}
			});
			await vi.advanceTimersByTimeAsync(4900);
			expect(button()).toBeNull();
			await vi.advanceTimersByTimeAsync(200);
			expect(signal?.aborted).toBe(true);
			expect(button()).not.toBeNull();
			expect(stored()).not.toBeNull();
		});
	});

	describe('an entry that is not valid', () => {
		it('older than 24 h: nothing is offered, nobody is asked and the entry is removed', async () => {
			rememberRecentRoom('KT7P', ID, Date.now() - RECENT_ROOM_TTL_MS - 1000);
			const load = vi.fn();
			render(ResumeCard, { onresume: vi.fn(), load });
			await Promise.resolve();
			expect(button()).toBeNull();
			expect(load).not.toHaveBeenCalled();
			expect(stored()).toBeNull();
		});

		it('corrupt JSON: nothing is offered and Home does not break', async () => {
			localStorage.setItem(RECENT_ROOM_KEY, '{"code":');
			const load = vi.fn();
			expect(() => render(ResumeCard, { onresume: vi.fn(), load })).not.toThrow();
			await Promise.resolve();
			expect(button()).toBeNull();
			expect(load).not.toHaveBeenCalled();
		});
	});

	it('leaving Home before the answer arrives changes nothing', async () => {
		rememberRecentRoom('KT7P', ID);
		const pending = deferred<RoomInfo>();
		const { unmount } = render(ResumeCard, { onresume: vi.fn(), load: () => pending.promise });
		unmount();
		pending.reject(new ApiError(404, 'room_not_found'));
		await Promise.resolve();
		await Promise.resolve();
		expect(stored()).not.toBeNull();
	});
});
