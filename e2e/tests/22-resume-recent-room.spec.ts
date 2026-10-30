import type { Page } from '@playwright/test';
import { expectAccessible } from '../src/a11y';
import { BASE_URL } from '../src/env';
import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode, relaunchBrowser, startGame } from '../src/flows';
import { expectUnobscured } from '../src/layout';
import { expectNoSpanish } from '../src/no-spanish';
import type { Player } from '../src/player';

test.describe.configure({ mode: 'parallel' });

const RECENT_ROOM = 'tf.recentRoom';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/**
 * "Resume recent game" (docs/ANDROID-PLAN.md 5.2). Android can kill the browser process; the per-tab
 * sessionStorage marker of the room goes with it, so Home offers a way back: the card shows only when
 * localStorage remembers a room that the server still knows under the same instance id, and tapping it goes
 * through the room route and the server's seat reclamation (guest cookie). "Process death" here is a NEW
 * browser context with the old cookies and localStorage (`storageState`) and a fresh sessionStorage.
 */

async function roomInfo(
	code: string
): Promise<{ status: number; players?: number; roomInstanceId?: string }> {
	const response = await fetch(`${BASE_URL}/api/rooms/${code}`);
	return response.status === 200
		? { status: 200, ...((await response.json()) as { players: number; roomInstanceId: string }) }
		: { status: response.status };
}

const stored = (page: Page) => page.evaluate((key) => localStorage.getItem(key), RECENT_ROOM);
const tabMarker = (page: Page, code: string) =>
	page.evaluate((key) => sessionStorage.getItem(key), `tf.room.${code}`);

async function expectOnHomeWithoutResume(player: Player): Promise<void> {
	await player.home.open();
	// give the preflight time to answer: the card must not appear after it
	await expect
		.poll(() => stored(player.page), { message: 'the stale entry is forgotten' })
		.toBeNull();
	await expect(player.home.resumeButton()).toHaveCount(0);
}

test('a killed browser comes back to the lobby through Resume recent game, with the same seat', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await ana.lobby.expectPlayers(['Ana', 'Beto']);

	// what the browser remembers: the code, the room instance id and the time. Nothing else.
	const saved = JSON.parse((await stored(ana.page)) ?? 'null') as Record<string, unknown>;
	expect(Object.keys(saved).sort()).toEqual(['code', 'lastJoinedAt', 'roomInstanceId']);
	expect(saved.code).toBe(code);
	expect(saved.roomInstanceId).toMatch(UUID);
	expect(saved.roomInstanceId).toBe((await roomInfo(code)).roomInstanceId);
	expect(Math.abs(Date.now() - Number(saved.lastJoinedAt))).toBeLessThan(120_000);
	expect(JSON.stringify(saved)).not.toContain('Ana');

	const reborn = await relaunchBrowser(app, ana);
	await reborn.home.open();
	// a new process has no tab marker: without Resume the invite gate would be next
	expect(await tabMarker(reborn.page, code)).toBeNull();

	const resume = reborn.home.resumeButton(code);
	await expect(resume).toBeVisible();
	await expect(resume).toHaveText(new RegExp(`${code}$`));
	// the card names the room and nobody in it
	await expect(resume).not.toContainText('Beto');
	await expect(resume).not.toContainText('Ana');
	const box = await resume.boundingBox();
	expect(box?.height ?? 0).toBeGreaterThanOrEqual(48);
	await expectAccessible(reborn, 'Home with the resume card');
	// nothing happened by itself: Resume only acts when it is tapped
	expect(new URL(reborn.page.url()).pathname).toBe('/');

	await resume.click();
	await reborn.page.waitForURL(`**/${code}`);
	await reborn.lobby.expectVisible(code);
	// the same seat (guest cookie), not a new one: still two players, Ana still the host, in join order
	await reborn.lobby.expectPlayers(['Ana', 'Beto']);
	await reborn.lobby.expectHost('Ana');
	await beto.lobby.expectPlayers(['Ana', 'Beto']);
	await beto.lobby.expectOffline('Ana', false);
	expect((await roomInfo(code)).players).toBe(2);
	expect(await tabMarker(reborn.page, code)).toBe('1');

	// leaving on purpose forgets the room: Home no longer offers it
	await reborn.lobby.leave();
	await expect(reborn.home.nickname).toBeVisible();
	expect(await stored(reborn.page)).toBeNull();
	await reborn.home.open();
	await expect(reborn.home.resumeButton()).toHaveCount(0);
});

test('a killed browser comes back to a game in progress, in the round that is being played', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, [ana, beto]);

	const reborn = await relaunchBrowser(app, ana);
	await reborn.home.open();
	await reborn.home.resumeButton(code).click();
	await reborn.expectScreen('letter');
	expect((await roomInfo(code)).players).toBe(2);
});

test('when the room is gone the card is not offered and the entry is forgotten', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const code = await createRoom(app, ana);
	const snapshot = await ana.context.storageState();
	expect(JSON.stringify(snapshot)).toContain(RECENT_ROOM);

	// the only player leaves on purpose: the room closes and its code stops existing
	await ana.lobby.leave();
	await expect(ana.home.nickname).toBeVisible();
	await expect.poll(async () => (await roomInfo(code)).status).toBe(404);

	// ... while a browser that was killed before still holds its entry from the snapshot
	await ana.context.close();
	const reborn = await app.player('Ana', { storageState: snapshot, acceptTerms: false });
	app.guard.allow({
		kind: 'response',
		url: /\/api\/rooms\//,
		status: 404,
		why: 'Home asks the server about the remembered room, which is gone'
	});
	app.guard.allow({
		kind: 'console',
		text: /Failed to load resource.*404/,
		why: 'the same lookup, as the browser logs it'
	});
	await expectOnHomeWithoutResume(reborn);
	// Home works as it always did
	await expect(reborn.home.createButton).toBeEnabled();
	await expect(reborn.home.joinButton).toBeDisabled();
});

test('a code that now belongs to another room is not offered either (different instance id)', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const code = await createRoom(app, ana);
	const snapshot = await ana.context.storageState();
	// the same code, but the browser remembers a room of another life
	for (const origin of snapshot.origins) {
		for (const item of origin.localStorage) {
			if (item.name !== RECENT_ROOM) continue;
			item.value = JSON.stringify({
				...(JSON.parse(item.value) as object),
				roomInstanceId: '11121314-1516-4718-991a-1b1c1d1e1f20'
			});
		}
	}
	await ana.context.close();

	const reborn = await app.player('Ana', { storageState: snapshot, acceptTerms: false });
	await expectOnHomeWithoutResume(reborn);
	// the real room still exists and its code still opens through the normal Join
	expect((await roomInfo(code)).status).toBe(200);
});

test('a corrupt or expired entry is ignored without breaking Home', async ({ app }) => {
	const ana = await app.player('Ana');
	await ana.home.open();
	for (const value of [
		'{"code":',
		JSON.stringify({
			code: 'KT7P',
			roomInstanceId: '01020304-0506-4708-890a-0b0c0d0e0f10',
			lastJoinedAt: Date.now() - 25 * 3600_000
		})
	]) {
		await ana.page.evaluate(({ key, v }) => localStorage.setItem(key, v), {
			key: RECENT_ROOM,
			v: value
		});
		await ana.page.reload();
		await expect(ana.home.nickname).toBeVisible();
		await expect.poll(() => stored(ana.page)).toBeNull();
		await expect(ana.home.resumeButton()).toHaveCount(0);
	}
});

for (const locale of ['es', 'en', 'nl'] as const) {
	test(`Home still fits a 360x740 phone with the card and Google sign-in on (${locale})`, async ({
		app
	}) => {
		// `/api/*` is faked with the service worker out of the way: the compose stack has no Google client
		const ana = await app.player('Ana', {
			locale,
			viewport: { width: 360, height: 740 },
			serviceWorkers: 'block'
		});
		const code = await createRoom(app, ana);
		await ana.page.route('**/api/session', async (route) => {
			const response = await route.fetch();
			await route.fulfill({
				response,
				json: { ...(await response.json()), googleEnabled: true }
			});
		});
		await ana.page.goto('/');
		await expect(ana.home.resumeButton(code)).toBeVisible();
		await expect(
			ana.page.getByRole('button', { name: ana.t('account_sign_in_google') })
		).toBeVisible();

		const resume = ana.home.resumeButton(code);
		await expectUnobscured(resume, 'Resume recent game');
		expect((await resume.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(47.5);
		const scroll = await ana.page.evaluate(
			() => document.documentElement.scrollHeight - window.innerHeight
		);
		expect(scroll, 'the Home screen must still fit one screen').toBeLessThanOrEqual(0);
		const overflow = await ana.page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(overflow, 'no sideways scrolling').toBeLessThanOrEqual(0);
		if (locale !== 'es') await expectNoSpanish([ana], locale, 'home');
	});
}
