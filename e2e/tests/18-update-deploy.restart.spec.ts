import { deployNewWorker, revertWorker } from '../src/compose';
import { COMPOSE_PROJECT } from '../src/env';
import { expect, test } from '../src/fixtures';
import { chooserStops, createRoom, joinByCode, startGame } from '../src/flows';

// eslint-disable-next-line playwright/no-skipped-test -- only runnable against a compose stack
test.skip(
	!COMPOSE_PROJECT,
	'E2E_COMPOSE_PROJECT is not set: the web container cannot be changed from here'
);

test.afterEach(() => revertWorker());

test('a deploy while a player is on the "new version" screen: Reload takes the new worker over and brings him back to the same room', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const code = await createRoom(app, ana);
	const beto = await app.player('Beto');
	const page = beto.page;
	const registration = () =>
		page.evaluate(async () => {
			const reg = await navigator.serviceWorker.getRegistration();
			return { waiting: reg?.waiting?.state ?? null, active: reg?.active?.state ?? null };
		});

	await test.step('Beto has the app installed and controlled by its worker', async () => {
		await beto.home.open();
		await beto.home.setNickname('Beto');
		await expect.poll(async () => (await registration()).active).toBe('activated');
		await page.reload();
		await expect
			.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null))
			.toBe(true);
		expect((await registration()).waiting).toBeNull();
	});

	// the server refuses his protocol version (close 4003) until the "new web image" is in place
	let mismatch = true;
	await page.routeWebSocket(/\/ws/, (socket) => {
		if (!mismatch) {
			socket.connectToServer();
			return;
		}
		socket.onMessage(() => socket.close({ code: 4003, reason: 'unsupported_version' }));
	});
	await page.evaluate(() => ((window as unknown as { tfMarker: number }).tfMarker = 1));

	await test.step('a new worker is deployed; the version screen finds it by itself and it waits', async () => {
		await deployNewWorker();
		await beto.home.submitCode(code);
		await expect(
			page.getByRole('heading', { name: beto.t('update_available'), exact: true })
		).toBeVisible();
		await expect
			.poll(async () => (await registration()).waiting, { timeout: 30_000 })
			.toBe('installed');
		// the banner at the top and the button of the screen
		await expect(page.getByRole('button', { name: beto.t('reload'), exact: true })).toHaveCount(2);
		expect(await page.evaluate(() => (window as unknown as { tfMarker?: number }).tfMarker)).toBe(
			1
		);
	});

	await test.step('Reload: the worker takes over, the page reloads once and Beto is in the room', async () => {
		mismatch = false;
		await page
			.getByRole('button', { name: beto.t('reload'), exact: true })
			.last()
			.click();
		await beto.lobby.expectVisible(code);
		expect(new URL(page.url()).pathname).toBe(`/${code}`);
		expect(
			await page.evaluate(() => (window as unknown as { tfMarker?: number }).tfMarker),
			'the page was reloaded'
		).toBeUndefined();
		expect(await registration(), 'the new worker is the active one, nothing waits').toEqual({
			waiting: null,
			active: 'activated'
		});
		await expect(page.getByRole('heading', { name: beto.t('update_available') })).toHaveCount(0);
		await expect(page.getByRole('button', { name: beto.t('reload'), exact: true })).toHaveCount(0);
	});
});

test.describe('two tabs of the same player', () => {
	/** Beto's room tab (B) and a second tab (A) on Home, both controlled by the same worker; a new worker is deployed and found. */
	async function twoTabs(app: import('../src/fixtures').App, play: boolean) {
		const ana = await app.player('Ana');
		const beto = await app.player('Beto');
		const code = await createRoom(app, ana);
		await joinByCode(beto, code);
		// first visit: the worker installs but only controls pages loaded after it
		await expect
			.poll(() =>
				beto.page.evaluate(
					async () => (await navigator.serviceWorker.getRegistration())?.active?.state
				)
			)
			.toBe('activated');
		await beto.page.reload();
		await beto.lobby.expectVisible(code);
		if (play) {
			await startGame(ana, [ana, beto]);
			await chooserStops([ana, beto]);
		}
		const tabB = beto.page;
		const tabA = await beto.newTab();
		await tabA.goto('/');
		for (const tab of [tabA, tabB]) {
			await expect
				.poll(() => tab.evaluate(() => navigator.serviceWorker.controller !== null))
				.toBe(true);
			await tab.evaluate(() => ((window as unknown as { tfMarker: number }).tfMarker = 1));
		}
		await deployNewWorker();
		await tabA.reload(); // a navigation: the browser looks for a new worker script
		for (const tab of [tabA, tabB]) {
			await expect(
				tab.getByRole('status').filter({ hasText: beto.t('update_available') })
			).toBeVisible({ timeout: 30_000 });
		}
		await tabA.evaluate(() => ((window as unknown as { tfMarker: number }).tfMarker = 1));
		const marker = (tab: typeof tabA) =>
			tab
				.evaluate(() => (window as unknown as { tfMarker?: number }).tfMarker)
				.catch(() => undefined); // the page is being replaced: reloaded
		const reload = (tab: typeof tabA) =>
			tab.getByRole('button', { name: beto.t('reload'), exact: true });
		return { ana, beto, code, tabA, tabB, marker, reload };
	}

	test('the tab that accepts the update takes a tab that is just in the lobby along', async ({
		app
	}) => {
		const { beto, code, tabA, tabB, marker, reload } = await twoTabs(app, false);
		await reload(tabA).click();
		await expect.poll(() => marker(tabA)).toBeUndefined();
		await expect.poll(() => marker(tabB), 'the other tab follows').toBeUndefined();
		await beto.lobby.expectVisible(code);
	});

	test('but it never reloads a tab in the middle of a round: the banner stays and the player decides', async ({
		app
	}) => {
		const { beto, tabA, tabB, marker, reload } = await twoTabs(app, true);
		// observe the new worker taking over this tab: a page that was reloaded loses the flag
		await tabB.evaluate(() => {
			navigator.serviceWorker.addEventListener('controllerchange', () => {
				(window as unknown as { tfControllerChanged: boolean }).tfControllerChanged = true;
			});
		});
		await reload(tabA).click();
		await expect.poll(() => marker(tabA)).toBeUndefined();

		// the playing tab has the new worker in charge now, but stays as it is
		await expect
			.poll(
				() =>
					tabB
						.evaluate(
							() => (window as unknown as { tfControllerChanged?: boolean }).tfControllerChanged
						)
						.catch(() => undefined),
				'the new worker takes over the playing tab, which is not reloaded'
			)
			.toBe(true);
		await expect(
			tabB.getByRole('status').filter({ hasText: beto.t('update_available') })
		).toBeVisible();
		expect(await marker(tabB), 'not reloaded behind the player').toBe(1);
		await beto.expectScreen('playing');

		// his own tap reloads him and the round comes back
		await reload(tabB).click();
		await expect.poll(() => marker(tabB)).toBeUndefined();
		await beto.expectScreen('playing');
	});
});
