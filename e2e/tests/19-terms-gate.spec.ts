import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode } from '../src/flows';
import { ORIGIN } from '../src/env';

test.describe.configure({ mode: 'parallel' });

/**
 * The terms gate (docs/DESIGN.md, "Content safety"): a guest that has not accepted the terms is asked
 * before it creates or joins a game, and the interrupted action simply continues. Every other spec plays
 * with the terms accepted (`acceptTerms` of `src/fixtures.ts`); the players here are first visits.
 */
const ROOMS = /\/api\/rooms$/;

test('a first visit is asked for the terms before it creates a game, then the game is created', async ({
	app
}) => {
	const ana = await app.player('Ana', { acceptTerms: false });
	const created: string[] = [];
	ana.page.on('request', (request) => {
		if (request.method() === 'POST' && ROOMS.test(request.url())) created.push(request.url());
	});

	await ana.home.open();
	await ana.home.setNickname('Ana');
	await app.beforeRoomCreation();
	await ana.home.createButton.click();

	await test.step('the gate stops the creation: nothing is created, nobody left Home', async () => {
		await ana.terms.expectOpen();
		await expect(ana.page).toHaveURL(/\/$/);
		expect(created).toEqual([]);
		// the link to the full terms is a real page load in this same browsing context (the Android app comes back with Back)
		await expect(ana.terms.termsLink).toHaveAttribute('href', '/terms#es');
		await expect(ana.terms.termsLink).toHaveAttribute('rel', 'external');
		await expect(ana.terms.termsLink).not.toHaveAttribute('target', /.+/);
		for (const button of [ana.terms.acceptButton, ana.terms.notNowButton]) {
			// the sheet pops in with a scale animation: measured once it settled
			await expect
				.poll(async () => (await button.boundingBox())!.height)
				.toBeGreaterThanOrEqual(47.5);
		}
	});

	await test.step('Accept without the box says so and sends nothing', async () => {
		let accepts = 0;
		ana.page.on('request', (r) => {
			if (/\/api\/terms\/accept$/.test(r.url())) accepts++;
		});
		await ana.terms.acceptButton.click();
		await expect(ana.terms.dialog.getByRole('alert')).toHaveText(
			ana.t('terms_gate_check_required')
		);
		expect(accepts).toBe(0);
		await expect(ana.terms.checkbox).toBeFocused();
	});

	await test.step('ticking the box and accepting continues: the room is created and entered', async () => {
		const accepted = await ana.terms.accept();
		expect(accepted.body).toEqual({
			version: expect.stringMatching(/^\d{4}-\d{2}$/),
			locale: 'es',
			age13Confirmed: true
		});
		expect((await accepted.request.allHeaders())['origin']).toBe(ORIGIN);
		await ana.page.waitForURL(/\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/);
		await ana.lobby.expectVisible(ana.code);
		expect(created).toHaveLength(1);
		await ana.terms.expectClosed();
	});
});

test('the terms come before joining by code and by invite link; declining joins nothing', async ({
	app
}) => {
	const host = await app.player('Ana');
	const code = await createRoom(app, host);
	const beto = await app.player('Beto', { acceptTerms: false, locale: 'en' });

	await test.step('by code on Home: the room is checked, the gate opens, nobody is in the room yet', async () => {
		await beto.home.open();
		await beto.home.setNickname('Beto');
		await beto.home.submitCode(code);
		await beto.terms.expectOpen();
		await expect(beto.page).toHaveURL(/\/$/);
		await host.lobby.expectPlayers(['Ana']);
	});

	await test.step('"Not now" joins nothing and frees Home', async () => {
		await beto.terms.notNowButton.click();
		await beto.terms.expectClosed();
		await expect(beto.page).toHaveURL(/\/$/);
		await expect(beto.home.joinButton).toBeEnabled();
		await host.lobby.expectPlayers(['Ana']);
	});

	await test.step('trying again and accepting joins, with the language of the screen', async () => {
		await beto.home.joinButton.click();
		await beto.terms.expectOpen();
		const accepted = await beto.terms.accept();
		expect(accepted.body.locale).toBe('en');
		await beto.lobby.expectVisible(code);
		await host.lobby.expectPlayers(['Ana', 'Beto']);
	});

	await test.step('by invite link: Join on the gate asks first; Not now stays on the gate', async () => {
		const cleo = await app.player('Cleo', { acceptTerms: false, locale: 'nl' });
		await cleo.page.goto(`/${code.toLowerCase()}`);
		await cleo.gate.expectOpen(code);
		await cleo.gate.setNickname('Cleo');
		await cleo.gate.join();
		await cleo.terms.expectOpen();
		await cleo.terms.notNowButton.click();
		await cleo.terms.expectClosed();
		await cleo.gate.expectOpen(code);
		await host.lobby.expectPlayers(['Ana', 'Beto']);
		await cleo.gate.join();
		await cleo.terms.expectOpen();
		await cleo.terms.accept();
		await cleo.lobby.expectVisible(code);
		await host.lobby.expectPlayers(['Ana', 'Beto', 'Cleo']);
	});
});

test('accepting is remembered: after a reload and for the next create or join there is no gate', async ({
	app
}) => {
	const dani = await app.player('Dani', { acceptTerms: false });
	await dani.home.open();
	await dani.home.setNickname('Dani');
	await app.beforeRoomCreation();
	await dani.home.createButton.click();
	await dani.terms.expectOpen();
	await dani.terms.accept();
	await dani.page.waitForURL(/\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/);
	await dani.lobby.expectVisible(dani.code);

	await test.step('the server knows it: the session says so', async () => {
		const session = await dani.page.request.get('/api/session');
		const { terms } = (await session.json()) as {
			terms: {
				requiredVersion: string;
				acceptedVersion: string | null;
				acceptanceRequired: boolean;
			};
		};
		expect(terms.acceptanceRequired).toBe(false);
		expect(terms.acceptedVersion).toBe(terms.requiredVersion);
	});

	await test.step('Home after a reload: creating again goes straight through', async () => {
		await dani.page.goto('/');
		await dani.page.reload();
		await expect(dani.home.nickname).toBeVisible();
		await app.beforeRoomCreation();
		await dani.home.createButton.click();
		await dani.page.waitForURL(/\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/);
		await dani.lobby.expectVisible(dani.code);
		await dani.terms.expectClosed();
	});

	await test.step('joining a room needs no gate either', async () => {
		const host = await app.player('Ana');
		const code = await createRoom(app, host);
		await joinByCode(dani, code);
		await host.lobby.expectPlayers(['Ana', 'Dani']);
		await dani.terms.expectClosed();
	});
});

test('a client that believes it accepted but did not (a new version, an expired acceptance): the server says so and the gate continues the creation', async ({
	app
}) => {
	// Without the service worker so that the test can rewrite what the session says
	const eva = await app.player('Eva', { acceptTerms: false, serviceWorkers: 'block' });
	await eva.page.route('**/api/session', async (route) => {
		const response = await route.fetch();
		const body = (await response.json()) as { terms: Record<string, unknown> };
		body.terms = {
			...body.terms,
			acceptedVersion: body.terms.requiredVersion,
			acceptanceRequired: false
		};
		await route.fulfill({ response, json: body });
	});
	app.guard.allow({
		kind: 'response',
		url: ROOMS,
		status: 403,
		why: 'the client believed the terms were accepted: the server refuses the creation with terms_required'
	});

	await eva.home.open();
	await eva.home.setNickname('Eva');
	await app.beforeRoomCreation();
	await eva.home.createButton.click();
	// no gate before the request (the client thinks it is fine), then the 403 brings it up
	await eva.terms.expectOpen();
	await expect(eva.page).toHaveURL(/\/$/);
	await eva.terms.accept();
	await eva.page.waitForURL(/\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/);
	await eva.lobby.expectVisible(eva.code);
});

test('the same for a join answered terms_required on the WebSocket: the gate, then the join once more', async ({
	app
}) => {
	const host = await app.player('Ana');
	const code = await createRoom(app, host);
	const fede = await app.player('Fede', { acceptTerms: false, serviceWorkers: 'block' });
	await fede.page.route('**/api/session', async (route) => {
		const response = await route.fetch();
		const body = (await response.json()) as { terms: Record<string, unknown> };
		body.terms = {
			...body.terms,
			acceptedVersion: body.terms.requiredVersion,
			acceptanceRequired: false
		};
		await route.fulfill({ response, json: body });
	});

	await fede.home.open();
	await fede.home.setNickname('Fede');
	await fede.home.submitCode(code);
	await fede.terms.expectOpen();
	// not an error screen behind the gate, and nobody joined yet
	await expect(
		fede.page.getByRole('heading', { name: fede.t('closed_rejected_title') })
	).toHaveCount(0);
	await host.lobby.expectPlayers(['Ana']);
	await fede.terms.accept();
	await fede.lobby.expectVisible(code);
	await host.lobby.expectPlayers(['Ana', 'Fede']);
});
