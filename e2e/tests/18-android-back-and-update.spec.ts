import { ORIGIN } from '../src/env';
import { expect, test, type App } from '../src/fixtures';
import { createRoom } from '../src/flows';

/**
 * Android Back + modal sheets, and the "new version" screen of the unified update flow
 * (docs/DESIGN.md, "Updates and the Android app"). A real service worker update cannot be staged here
 * (Playwright does not see the worker script's update fetch, and a second build is not available): that
 * chain is covered by the unit tests of `web/src/lib/pwa/update.svelte.ts` and by a manual run with two builds.
 */

test.describe('the Back button and modal sheets', () => {
	test('Back closes the QR sheet and the leave sheet without leaving the room, and the history does not grow', async ({
		app
	}) => {
		const ana = await app.player('Ana');
		const code = await createRoom(app, ana);
		const page = ana.page;
		const historyLength = () => page.evaluate(() => history.length);
		const atRoom = async () => {
			expect(new URL(page.url()).pathname).toBe(`/${code}`);
			await ana.lobby.expectVisible(code);
		};

		const start = await historyLength();

		await test.step('the QR sheet: Back closes it, the room stays', async () => {
			await ana.lobby.qrButton.click();
			await expect(ana.lobby.qrDialog).toBeVisible();
			expect(await historyLength(), 'opening pushed exactly one entry').toBe(start + 1);
			await page.goBack();
			await expect(ana.lobby.qrDialog).toBeHidden();
			await atRoom();
		});

		await test.step('the leave confirmation: Back is "stay", nothing is sent', async () => {
			await ana.lobby.openLeave();
			await page.goBack();
			await expect(ana.lobby.leaveDialog).toBeHidden();
			await atRoom();
			expect(ana.sentCount('leave'), 'Back never leaves the game').toBe(0);
		});

		await test.step('closing with the buttons pops the entry again: three cycles add one entry at most', async () => {
			for (let i = 0; i < 3; i++) {
				await ana.lobby.qrButton.click();
				await expect(ana.lobby.qrDialog).toBeVisible();
				await ana.lobby.closeQr();
			}
			await ana.lobby.openLeave();
			await ana.lobby.stay();
			await atRoom();
			expect(await historyLength(), 'no dead entries left behind').toBeLessThanOrEqual(start + 1);
		});

		await test.step('with no sheet open Back is the normal Back: it leaves the room page for Home', async () => {
			await page.goBack();
			await expect(ana.home.nickname).toBeVisible();
			expect(new URL(page.url()).pathname).toBe('/');
			expect(ana.sentCount('leave')).toBe(0);
		});
	});

	test('Escape closes a sheet and also pops its history entry', async ({ app }) => {
		const ana = await app.player('Ana');
		await createRoom(app, ana);
		await ana.lobby.qrButton.click();
		await expect(ana.lobby.qrDialog).toBeVisible();
		await ana.page.keyboard.press('Escape');
		await expect(ana.lobby.qrDialog).toBeHidden();
		// the entry was popped (not left behind): one Back from here goes straight to Home
		await ana.page.goBack();
		await expect(ana.home.nickname).toBeVisible();
	});
});

test.describe('the "new version" screen (protocol mismatch)', () => {
	/**
	 * Beto's tab holds a room marker and the server refuses his protocol version (close 4003, played
	 * by `routeWebSocket`: the stack's server speaks the version of this build). He has a working
	 * service worker. There is NO newer worker to install (one cannot be built inside a test), which is
	 * exactly the case that used to loop: a plain reload brought the old shell back from the worker.
	 */
	async function mismatchScreen(app: App) {
		const host = await app.player('Ana');
		const code = await createRoom(app, host);
		const beto = await app.player('Beto', { network: true });
		await beto.home.open();
		await beto.home.setNickname('Beto');
		await expect
			.poll(() =>
				beto.page.evaluate(
					async () => (await navigator.serviceWorker.getRegistration())?.active?.state
				)
			)
			.toBe('activated');
		await beto.page.reload();
		await expect
			.poll(() => beto.page.evaluate(() => navigator.serviceWorker.controller !== null))
			.toBe(true);
		await beto.page.routeWebSocket(/\/ws/, (socket) => {
			socket.onMessage(() => socket.close({ code: 4003, reason: 'unsupported_version' }));
		});
		await beto.page.evaluate(() => ((window as unknown as { tfMarker: number }).tfMarker = 1));
		await beto.home.submitCode(code);
		await expect(
			beto.page.getByRole('heading', { name: beto.t('update_available'), exact: true })
		).toBeVisible();
		await expect(beto.page.getByText(beto.t('closed_version_text'), { exact: true })).toBeVisible();
		return { beto, code };
	}
	const stillTheSamePage = (page: import('@playwright/test').Page) =>
		page.evaluate(() => (window as unknown as { tfMarker?: number }).tfMarker === 1);

	test('with nothing newer to install, Reload says so instead of reloading into the same screen', async ({
		app
	}) => {
		const { beto, code } = await mismatchScreen(app);
		const reload = beto.page.getByRole('button', { name: beto.t('reload'), exact: true });
		const status = beto.page.getByRole('status').filter({ hasText: beto.t('update_not_ready') });

		for (let tap = 0; tap < 2; tap++) {
			await reload.click();
			await expect(status).toBeVisible();
			expect(await stillTheSamePage(beto.page), 'the page was not reloaded').toBe(true);
			await expect(reload).toBeEnabled();
		}
		expect(new URL(beto.page.url()).pathname).toBe(`/${code}`);
		expect(
			await beto.page.evaluate((c) => sessionStorage.getItem(`tf.room.${c}`), code),
			'the room marker of the tab is still there'
		).toBe('1');
	});

	test('offline it says so (a polite status, a 48 px button) and recovers when the network is back', async ({
		app
	}) => {
		const { beto } = await mismatchScreen(app);
		const reload = beto.page.getByRole('button', { name: beto.t('reload'), exact: true });
		const offline = beto.page.getByRole('status').filter({ hasText: beto.t('update_offline') });

		await beto.network?.drop();
		await reload.click();
		await expect(offline).toBeVisible();
		await expect(offline).toHaveAttribute('aria-live', 'polite');
		expect(await stillTheSamePage(beto.page)).toBe(true);
		expect((await reload.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(48);

		await beto.network?.restore();
		await expect(offline).toHaveCount(0);
		expect(await stillTheSamePage(beto.page)).toBe(true);
	});
});

test('Digital Asset Links: the file is JSON that is always revalidated, and /.well-known/ never answers with the app', async ({
	request
}) => {
	const url = (path: string) => new URL(path, ORIGIN).toString();

	const links = await request.get(url('/.well-known/assetlinks.json'), { maxRedirects: 0 });
	expect(links.status(), 'no redirect, no fallback').toBe(200);
	expect(links.headers()['content-type']).toMatch(/^application\/json/);
	expect(links.headers()['cache-control']).toBe('no-cache');
	expect(links.headers()['x-content-type-options']).toBe('nosniff');
	const statements = (await links.json()) as { target?: { package_name?: string } }[];
	expect(Array.isArray(statements)).toBe(true);
	for (const statement of statements) {
		expect(statement.target?.package_name).toBe('xyz.aresius.tuttifrutti');
	}

	for (const path of ['/.well-known/other', '/.well-known/', '/.well-known/assetlinks.json/x']) {
		const response = await request.get(url(path), { maxRedirects: 0 });
		expect(response.status(), path).toBe(404);
		expect(await response.text(), `${path} is not the SPA shell`).not.toContain('<!doctype html>');
		expect(response.headers()['x-content-type-options'], path).toBe('nosniff');
	}
});
