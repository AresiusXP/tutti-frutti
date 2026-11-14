import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import { bastaBy, chooserStops, fillAnswers, openRound, uniqueAnswers } from '../src/flows';
import { expectUnobscured } from '../src/layout';
import type { Locale } from '../src/messages';
import { expectNoSpanish, type NonSpanishLocale } from '../src/no-spanish';

test.describe.configure({ mode: 'parallel' });

/**
 * The "How to play" guide (a sheet, opened from Home, the lobby, the letter and playing screens, the review
 * header and the summary) and the one-time tips on the playing and review screens (docs/DESIGN.md, "How to play
 * guide and tips"). Every other spec starts with the tips already seen (`PlayerOptions.tips`).
 */

for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`Home: the guide opens on a small phone, is accessible and speaks ${locale}`, async ({
		app
	}) => {
		const ana = await app.player('Ana', {
			locale,
			reducedMotion: true,
			viewport: { width: 360, height: 740 }
		});
		await ana.home.open();
		await expectUnobscured(ana.help.buttons.first(), 'the help button of Home');
		const box = await ana.help.buttons.first().boundingBox();
		expect(box?.width ?? 0, 'touch target').toBeGreaterThanOrEqual(47.5);
		expect(box?.height ?? 0, 'touch target').toBeGreaterThanOrEqual(47.5);

		await ana.help.open();
		await expect(ana.help.dialog).toBeVisible();
		await expect(ana.help.openPart).toHaveCount(1);
		await expect(ana.help.part('basics')).toHaveAttribute('aria-expanded', 'true');
		await expectAccessible(ana, 'the how-to-play guide');

		// every part opens, one at a time
		for (const id of ['playing', 'review', 'points'] as const) {
			await ana.help.part(id).click();
			await expect(ana.help.openPart).toHaveCount(1);
			await expect(ana.help.part(id)).toHaveAttribute('aria-expanded', 'true');
		}
		await expectAccessible(ana, 'the how-to-play guide, points');
		if (locale !== 'es') await expectNoSpanish([ana], locale as NonSpanishLocale, 'the guide');

		await ana.help.close();
		await expect(ana.help.dialog).toBeHidden();
	});
}

test('Android Back closes the guide without leaving the page, and the history does not grow', async ({
	app
}) => {
	const ana = await app.player('Ana');
	await ana.home.open();
	const length = () => ana.page.evaluate(() => history.length);
	const start = await length();

	await ana.help.open();
	await expect(ana.help.dialog).toBeVisible();
	expect(await length(), 'opening pushed exactly one entry').toBe(start + 1);
	await ana.page.goBack();
	await expect(ana.help.dialog).toBeHidden();
	await expect(ana.home.nickname).toBeVisible();
	expect(new URL(ana.page.url()).pathname).toBe('/');
});

test('the first-time tips show once: playing, then review, then never again', async ({ app }) => {
	const { players, host } = await openRound(app, {
		playerOptions: () => ({ tips: true })
	});
	const [ana, ben, cleo] = players as [
		(typeof players)[0],
		(typeof players)[0],
		(typeof players)[0]
	];

	await test.step('everybody gets the playing tip; closing it is remembered for that player only', async () => {
		for (const p of players) await expect(p.help.tip).toContainText(p.t('help_tip_playing'));
		await ana.help.dismissTip();
		await expect(ana.help.tip).toBeHidden();
		await expect(ben.help.tip).toBeVisible();
	});

	await test.step('the tip does not stop anybody from playing', async () => {
		const { letter } = await ana.playing.roundInfo();
		await fillAnswers(ana, uniqueAnswers(letter, ana, 6));
		await bastaBy(ana, [ben, cleo]);
		await Promise.all(players.map((p) => p.expectScreen('review')));
	});

	await test.step('on the review screen everybody who can vote gets the review tip', async () => {
		for (const p of players) await expect(p.help.tip).toContainText(p.t('help_tip_review'));
		// one card, not two: the playing tip Ben left open counted as seen when its screen went away
		await expect(ben.help.tip).toHaveCount(1);
	});

	await test.step('its "How to play" button opens the guide at the review part', async () => {
		await ben.help.tip.getByRole('button', { name: ben.t('help_title'), exact: true }).click();
		await expect(ben.help.dialog).toBeVisible();
		await expect(ben.help.part('review')).toHaveAttribute('aria-expanded', 'true');
		await ben.help.close();
		await expect(ben.help.tip).toBeVisible();
	});

	await test.step('the next round and its review have no tips', async () => {
		await host.review.finish({ anyway: true });
		await Promise.all(players.map((p) => p.expectScreen('summary')));
		await host.summary.nextRound();
		await chooserStops(players);
		for (const p of players) await expect(p.help.tip).toHaveCount(0);
		const { letter } = await ana.playing.roundInfo();
		await fillAnswers(ana, uniqueAnswers(letter, ana, 6));
		await bastaBy(ana, [ben, cleo]);
		await Promise.all(players.map((p) => p.expectScreen('review')));
		for (const p of players) await expect(p.help.tip).toHaveCount(0);
	});
});

test('the review header and the summary open the guide at their own part', async ({ app }) => {
	const { players, host } = await openRound(app, { locale: 'en' });
	const [ana, ben, cleo] = players as [
		(typeof players)[0],
		(typeof players)[0],
		(typeof players)[0]
	];
	const { letter } = await ana.playing.roundInfo();
	await fillAnswers(ana, uniqueAnswers(letter, ana, 6));
	await bastaBy(ana, [ben, cleo]);
	await Promise.all(players.map((p) => p.expectScreen('review')));

	for (const p of players) await expect(p.help.tip).toHaveCount(0); // seen: the default for every player
	await ben.help.open();
	await expect(ben.help.part('review')).toHaveAttribute('aria-expanded', 'true');
	await ben.help.close();

	await host.review.finish({ anyway: true });
	await Promise.all(players.map((p) => p.expectScreen('summary')));
	await ben.help.open();
	await expect(ben.help.part('points')).toHaveAttribute('aria-expanded', 'true');
	await expectAccessible(ben, 'the guide on the summary');
});
