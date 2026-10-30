// @vitest-environment jsdom
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView } from '$lib/game/fixtures';
import { markRoom } from '$lib/game/roomMark';
import type { Phase } from '$lib/protocol';
import { profile } from '$lib/profile.svelte';
import { installFakeServiceWorker, restoreFakeServiceWorker } from '$lib/pwa/sw.testkit';
import { resetUpdateForTests, startUpdates } from '$lib/pwa/update.svelte';
import RoomRoute from './RoomRoute.svelte';

const plugin = vi.hoisted(() => ({ onNeedReload: undefined as (() => void) | undefined }));
vi.mock('virtual:pwa-register', () => ({
	registerSW: (options: { onNeedReload?: () => void }) => {
		plugin.onNeedReload = options.onNeedReload;
		return () => Promise.resolve();
	}
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));
vi.mock('$lib/screens/RoomScreen.svelte', async () => ({
	default: (await import('./RoomScreenStub.svelte')).default
}));

describe('the room route and an update accepted in another tab', () => {
	let reload: ReturnType<typeof installFakeServiceWorker>['reload'];
	let stop: () => void;

	beforeEach(async () => {
		resetUpdateForTests();
		sessionStorage.clear();
		({ reload } = installFakeServiceWorker());
		profile.setNickname('Ana');
		markRoom('KT7P');
		stop = startUpdates();
		await vi.waitFor(() => expect(plugin.onNeedReload).toBeDefined());
	});
	afterEach(() => {
		stop();
		restoreFakeServiceWorker();
	});

	const open = (phase: Phase) => {
		const session = new FakeSession(makeView({ phase }));
		render(RoomRoute, { code: 'KT7P', createSession: () => session });
		return session;
	};

	it.each<Phase>(['playing', 'grace'])('does not reload a tab in the %s phase', (phase) => {
		open(phase);
		plugin.onNeedReload?.();
		expect(reload).not.toHaveBeenCalled();
	});

	it.each<Phase>(['lobby', 'letter', 'review', 'round_summary', 'final'])(
		'reloads a tab in the %s phase',
		(phase) => {
			open(phase);
			plugin.onNeedReload?.();
			expect(reload).toHaveBeenCalledOnce();
		}
	);

	it('follows the session: held while the round runs, free once it moved on or closed', async () => {
		const session = open('playing');
		plugin.onNeedReload?.();
		expect(reload).not.toHaveBeenCalled();
		session.setView(makeView({ phase: 'review' }));
		await tick();
		plugin.onNeedReload?.();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('a closed session (the version screen itself) no longer holds the reload', () => {
		const session = open('playing');
		session.status = 'closed';
		plugin.onNeedReload?.();
		expect(reload).toHaveBeenCalledOnce();
	});

	it('leaving the route releases the hold', () => {
		const { unmount } = render(RoomRoute, {
			code: 'KT7P',
			createSession: () => new FakeSession(makeView({ phase: 'playing' }))
		});
		unmount();
		plugin.onNeedReload?.();
		expect(reload).toHaveBeenCalledOnce();
	});
});
