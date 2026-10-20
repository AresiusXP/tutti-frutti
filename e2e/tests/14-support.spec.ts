import type { Locator } from '@playwright/test';
import { expectAccessible } from '../src/a11y';
import { BASE_URL } from '../src/env';
import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode, quickRound, startGame } from '../src/flows';
import { expectUnobscured } from '../src/layout';
import type { Locale } from '../src/messages';
import { expectNoSpanish } from '../src/no-spanish';
import type { Player } from '../src/player';

test.describe.configure({ mode: 'parallel' });

const CAFECITO = 'https://cafecito.app/aresiusxp';
const KOFI = 'https://ko-fi.com/aresiusxp';

/**
 * The "support the game" link goes to Cafecito for a player in Argentina and to Ko-fi for anybody else.
 * The browser decides from the device timezone and language list (nothing is asked of the server, no
 * IP lookup), so each case is a different emulated device. The screen language only changes the label.
 * The links are never opened: they are third-party sites, and checking the address is the point.
 */
const DEVICES: {
	name: string;
	locale: Locale;
	browserLanguage: string;
	timezoneId: string;
	url: string;
}[] = [
	{
		name: 'Buenos Aires, es-AR',
		locale: 'es',
		browserLanguage: 'es-AR',
		timezoneId: 'America/Argentina/Buenos_Aires',
		url: CAFECITO
	},
	{
		name: 'an English screen in Córdoba',
		locale: 'en',
		browserLanguage: 'en-US',
		timezoneId: 'America/Argentina/Cordoba',
		url: CAFECITO
	},
	{
		name: 'a legacy Argentine zone name, Dutch screen',
		locale: 'nl',
		browserLanguage: 'nl-NL',
		timezoneId: 'America/Buenos_Aires',
		url: CAFECITO
	},
	{
		name: 'Amsterdam, nl-NL',
		locale: 'nl',
		browserLanguage: 'nl-NL',
		timezoneId: 'Europe/Amsterdam',
		url: KOFI
	},
	{
		name: 'Amsterdam, English screen',
		locale: 'en',
		browserLanguage: 'en-GB',
		timezoneId: 'Europe/Amsterdam',
		url: KOFI
	},
	{
		name: 'Madrid, es-ES (Spanish but not Argentine)',
		locale: 'es',
		browserLanguage: 'es-ES',
		timezoneId: 'Europe/Madrid',
		url: KOFI
	},
	{
		name: 'Montevideo, es-UY (next door)',
		locale: 'es',
		browserLanguage: 'es-UY',
		timezoneId: 'America/Montevideo',
		url: KOFI
	}
];

/** The link is a plain outbound link to `url`, in a new tab, with no referrer. */
async function expectOutbound(player: Player, link: Locator, url: string) {
	await expect(link).toHaveAttribute('href', url);
	await expect(link).toHaveAttribute('target', '_blank');
	await expect(link).toHaveAttribute('rel', /\bnoopener\b/);
	await expect(link).toHaveAttribute('rel', /\bnoreferrer\b/);
	await expectUnobscured(link, `${player.name}'s support link`);
	const box = await link.boundingBox();
	expect(box?.height ?? 0, 'touch target').toBeGreaterThanOrEqual(47.5);
}

for (const device of DEVICES) {
	test(`Home: ${device.name} gets ${device.url.includes('cafecito') ? 'Cafecito' : 'Ko-fi'}`, async ({
		app
	}) => {
		const ana = await app.player('Ana', {
			locale: device.locale,
			browserLanguage: device.browserLanguage,
			timezoneId: device.timezoneId,
			// a small phone: the extra link must not push Home off the screen
			viewport: { width: 360, height: 740 }
		});
		const hosts = new Set<string>();
		ana.page.on('request', (request) => {
			const host = new URL(request.url()).host;
			if (host !== new URL(BASE_URL).host) hosts.add(host);
		});
		await ana.home.open();

		await expect(ana.home.supportLink).toBeVisible();
		await expect(ana.home.supportLink).toContainText(ana.t('support_link'));
		await expectOutbound(ana, ana.home.supportLink, device.url);
		await expect(ana.home.privacyLink).toBeVisible();
		await expectUnobscured(ana.home.privacyLink, 'the privacy link beside it');

		// both links share one row: the support link adds no height to Home
		const [privacyBox, supportBox] = await Promise.all([
			ana.home.privacyLink.boundingBox(),
			ana.home.supportLink.boundingBox()
		]);
		expect(Math.abs((privacyBox?.y ?? 0) - (supportBox?.y ?? 99)), 'same row').toBeLessThan(2);

		const scroll = await ana.page.evaluate(
			() => document.documentElement.scrollHeight - window.innerHeight
		);
		expect(scroll, 'the Home screen must still fit one screen').toBeLessThanOrEqual(0);
		const overflow = await ana.page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(overflow, 'no sideways scrolling').toBeLessThanOrEqual(0);

		if (device.locale !== 'es') await expectNoSpanish([ana], device.locale, 'home');
		// nothing leaves the device: opening Home talks only to the game's own host
		expect([...hosts], 'requests to other hosts').toEqual([]);
	});
}

for (const device of DEVICES.filter((d) =>
	['Buenos Aires, es-AR', 'Amsterdam, nl-NL'].includes(d.name)
)) {
	test(`Final: ${device.name} gets ${device.url.includes('cafecito') ? 'Cafecito' : 'Ko-fi'}, host and guest alike`, async ({
		app
	}) => {
		const options = {
			locale: device.locale,
			browserLanguage: device.browserLanguage,
			timezoneId: device.timezoneId,
			reducedMotion: true
		};
		const ana = await app.player('Ana', options);
		const beto = await app.player('Beto', options);
		const everyone = [ana, beto];

		const code = await createRoom(app, ana);
		await joinByCode(beto, code);
		await startGame(ana, everyone);
		await quickRound({ host: ana, everyone, caller: ana, fields: 6 });
		await ana.summary.endGame();

		for (const p of everyone) {
			await p.expectScreen('final');
			await p.final.supportLink.scrollIntoViewIfNeeded();
			await expectOutbound(p, p.final.supportLink, device.url);
			// the main actions stay where they were: the link is in the content, not the footer
			await expect(p.page.locator('footer').getByRole('link')).toHaveCount(0);
			if (device.locale !== 'es') await expectNoSpanish([p], device.locale, 'final');
			await expectAccessible(p, `final with the support link (${p === ana ? 'host' : 'guest'})`);
		}
		await expect(ana.final.rematchButton).toBeVisible();
		await expectUnobscured(ana.final.rematchButton, 'Rematch');
	});
}

test('the screen language does not choose the link: switching it keeps the same destination', async ({
	app
}) => {
	const ana = await app.player('Ana', {
		locale: 'nl',
		browserLanguage: 'nl-NL',
		timezoneId: 'Europe/Amsterdam'
	});
	await ana.home.open();
	await expect(ana.home.supportLink).toHaveAttribute('href', KOFI);

	for (const choice of ['es', 'en', 'nl'] as const) {
		await ana.home.switchLanguage(choice);
		await expect(ana.home.supportLink).toContainText(ana.t('support_link'));
		await expect(ana.home.supportLink).toHaveAttribute('href', KOFI);
	}
});
