import type { Page } from '@playwright/test';
import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode, quickRound, startGame } from '../src/flows';
import { expectUnobscured } from '../src/layout';
import type { Locale } from '../src/messages';
import { expectNoSpanish } from '../src/no-spanish';

test.describe.configure({ mode: 'parallel' });

const TWA = 'android-app://xyz.aresius.tuttifrutti';
const MARKER = 'tf.android';

/**
 * The Android app (a Trusted Web Activity) hides the Cafecito / Ko-fi links; the website keeps them. The app
 * is recognised by `document.referrer` of the first document of the task (`android-app://<package>`), which a
 * navigation cannot produce, so the test defines it before the page's own scripts run. The init script plays the
 * LAUNCH only: it sets the referrer on the first document of a tab and never again (a stamp in this tab's
 * sessionStorage), so what comes after is what a real session sees: reloads, the static pages and the way back
 * carry another referrer and the marker alone has to carry the context.
 */
async function launchLikeTheApp(page: Page, referrer: string = TWA): Promise<void> {
	await page.addInitScript((value) => {
		if (sessionStorage.getItem('e2e.launched')) return;
		sessionStorage.setItem('e2e.launched', '1');
		Object.defineProperty(document, 'referrer', { get: () => value, configurable: true });
	}, referrer);
}

const marker = (page: Page, storage: 'sessionStorage' | 'localStorage' = 'sessionStorage') =>
	page.evaluate(
		({ area, key }) => (area === 'sessionStorage' ? sessionStorage : localStorage).getItem(key),
		{ area: storage, key: MARKER }
	);

test('in the Android app Home has no support link, keeps Privacy, and still fits a 360x740 phone', async ({
	app
}) => {
	const ana = await app.player('Ana', { viewport: { width: 360, height: 740 } });
	await launchLikeTheApp(ana.page);
	await ana.home.open();

	expect(await marker(ana.page)).toBe('1');
	expect(await marker(ana.page, 'localStorage'), 'a flag the website would share').toBeNull();
	await expect(ana.home.supportLink).toHaveCount(0);
	await expect(ana.page.getByText(/cafecito|ko-fi/i)).toHaveCount(0);
	await expect(ana.home.privacyLink).toBeVisible();
	await expectUnobscured(ana.home.privacyLink, 'the privacy link, alone on its row');
	expect((await ana.home.privacyLink.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(47.5);
	await expect(ana.page.locator('a[target="_blank"]')).toHaveCount(0);

	const scroll = await ana.page.evaluate(
		() => document.documentElement.scrollHeight - window.innerHeight
	);
	expect(scroll, 'the Home screen must still fit one screen').toBeLessThanOrEqual(0);
	await expectAccessible(ana, 'Home inside the Android app');
});

for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`the marker survives a reload and the static pages, and the links stay away (${locale})`, async ({
		app
	}) => {
		const ana = await app.player('Ana', { locale });
		await launchLikeTheApp(ana.page);
		await ana.home.open();
		await expect(ana.home.supportLink).toHaveCount(0);

		// a real page load of a static page (the policy), then Back: the referrer is no longer android-app://
		await ana.home.privacyLink.click();
		await ana.page.waitForURL(new RegExp(`/privacy#${locale}$`));
		await ana.page.goBack();
		await expect(ana.home.nickname).toBeVisible();
		expect(await ana.page.evaluate(() => document.referrer.startsWith('android-app://'))).toBe(
			false
		);
		expect(await marker(ana.page)).toBe('1');
		await expect(ana.home.supportLink).toHaveCount(0);
		await expect(ana.home.privacyLink).toBeVisible();

		// and a plain reload of the same tab
		await ana.page.reload();
		await expect(ana.home.nickname).toBeVisible();
		await expect(ana.home.supportLink).toHaveCount(0);
		await expect(ana.home.privacyLink).toBeVisible();
		if (locale !== 'es') await expectNoSpanish([ana], locale, 'home');
	});
}

test('the marker belongs to the app tab: the website in another tab of the same browser keeps its links', async ({
	app
}) => {
	const ana = await app.player('Ana', { timezoneId: 'Europe/Amsterdam' });
	await launchLikeTheApp(ana.page);
	await ana.home.open();
	await expect(ana.home.supportLink).toHaveCount(0);

	const other = await ana.newTab();
	await other.goto('/');
	await expect(other.getByRole('link', { name: ana.t('support_link') })).toBeVisible();
	expect(await marker(other)).toBeNull();
	// and the app tab did not learn anything from it
	await ana.page.reload();
	await expect(ana.home.nickname).toBeVisible();
	await expect(ana.home.supportLink).toHaveCount(0);
});

test('the website, an installed-PWA-like launch from another app and a look-alike package keep the links', async ({
	app
}) => {
	const referrers = [
		'',
		'https://www.google.com/',
		'android-app://com.google.android.googlequicksearchbox',
		'android-app://xyz.aresius.tuttifrutti.evil',
		'android-app://xyz.aresius.tuttifruttix/'
	];
	for (const referrer of referrers) {
		const ana = await app.player('Ana');
		await launchLikeTheApp(ana.page, referrer);
		await ana.home.open();
		await expect(ana.home.supportLink, `referrer ${JSON.stringify(referrer)}`).toBeVisible();
		expect(await marker(ana.page)).toBeNull();
		await expect(ana.home.privacyLink).toBeVisible();
	}
});

test('on the Final screen the app has no support card at all, and the website has it', async ({
	app
}) => {
	const options = { reducedMotion: true };
	const ana = await app.player('Ana', options);
	const beto = await app.player('Beto', options);
	await launchLikeTheApp(ana.page);
	const everyone = [ana, beto];

	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, everyone);
	await quickRound({ host: ana, everyone, caller: ana, fields: 6 });
	await ana.summary.endGame();
	for (const p of everyone) await p.expectScreen('final');

	// Ana is the app: neither the link nor its sentence nor an empty card; the main actions are untouched
	await expect(ana.final.supportLink).toHaveCount(0);
	await expect(ana.page.getByText(ana.t('support_final_text'), { exact: true })).toHaveCount(0);
	await expect(ana.page.getByRole('region', { name: ana.t('support_link') })).toHaveCount(0);
	await expect(ana.final.rematchButton).toBeVisible();
	await expectUnobscured(ana.final.rematchButton, 'Rematch');
	await expectAccessible(ana, 'final inside the Android app');
	expect(await marker(ana.page)).toBe('1');

	// Beto is the website: unchanged
	await beto.final.supportLink.scrollIntoViewIfNeeded();
	await expect(beto.final.supportLink).toBeVisible();
	await expect(beto.page.getByText(beto.t('support_final_text'), { exact: true })).toBeVisible();
});

test('the marker is presentation only: nothing about it reaches the server', async ({ app }) => {
	const ana = await app.player('Ana');
	await launchLikeTheApp(ana.page);
	const seen: string[] = [];
	ana.page.on('request', (request) => {
		seen.push(`${request.url()} ${request.postData() ?? ''} ${JSON.stringify(request.headers())}`);
	});
	await createRoom(app, ana);
	const leaked = seen.filter((line) => /android-app|tf\.android|xyz\.aresius/i.test(line));
	expect(leaked).toEqual([]);
});
