// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import {
	FakeWorker,
	installFakeServiceWorker,
	restoreFakeServiceWorker
} from '$lib/pwa/sw.testkit';
import { resetUpdateForTests, update } from '$lib/pwa/update.svelte';
import UpdatePrompt from './UpdatePrompt.svelte';

const plugin = vi.hoisted(() => ({ registerSW: vi.fn() }));
vi.mock('virtual:pwa-register', () => ({ registerSW: plugin.registerSW }));

describe('UpdatePrompt', () => {
	let container: ReturnType<typeof installFakeServiceWorker>['container'];
	let reload: ReturnType<typeof installFakeServiceWorker>['reload'];

	beforeEach(() => {
		i18n.set('es');
		resetUpdateForTests();
		sessionStorage.clear();
		plugin.registerSW.mockReset();
		({ container, reload } = installFakeServiceWorker());
	});
	afterEach(() => restoreFakeServiceWorker());

	it('registers the worker once and shows nothing until a new version waits', async () => {
		const { unmount } = render(UpdatePrompt);
		await vi.waitFor(() => expect(plugin.registerSW).toHaveBeenCalledOnce());
		expect(screen.queryByRole('status')).toBeNull();
		update.needRefresh = true;
		await tick();
		expect(screen.getByRole('status').textContent).toContain('Nueva versión disponible');
		unmount();
	});

	it('Reload applies the update through the shared flow', async () => {
		const worker = new FakeWorker('installed');
		container.registration!.waiting = worker;
		update.needRefresh = true;
		render(UpdatePrompt);
		await fireEvent.click(screen.getByRole('button', { name: 'Recargar' }));
		await vi.waitFor(() =>
			expect(worker.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' })
		);
		expect(reload).not.toHaveBeenCalled();
		worker.moveTo('activated');
		await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce());
	});

	it('says why it cannot update (and is still operable)', async () => {
		update.needRefresh = true;
		render(UpdatePrompt);
		update.error = 'not_ready';
		await tick();
		expect(screen.getByRole('status').textContent).toContain(
			'La versión nueva todavía no está lista'
		);
		update.error = 'offline';
		await tick();
		expect(screen.getByRole('status').textContent).toContain('Estás sin conexión');
		const button = screen.getByRole('button', { name: 'Recargar' }) as HTMLButtonElement;
		expect(button.disabled).toBe(false);
		expect(button.className).toContain('min-h-12');
	});
});
