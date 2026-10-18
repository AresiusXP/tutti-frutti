import { expect, test } from '../src/fixtures';
import type { Locale } from '../src/messages';
import { expectNoSpanish } from '../src/no-spanish';
import type { Player } from '../src/player';

test.describe.configure({ mode: 'parallel' });

const SCREENS: Locale[] = ['es', 'en', 'nl'];

/** Home in the language the player now expects: <html lang>, its own labels and the switch pressed on it. */
async function expectHomeIn(player: Player, where: string): Promise<void> {
	const locale = player.locale;
	await expect(player.page.locator('html')).toHaveAttribute('lang', locale);
	await expect(player.home.nickname).toBeVisible();
	await expect(player.home.createButton).toBeVisible();
	await expect(player.home.joinButton).toBeVisible();
	const group = player.page.getByRole('group', { name: player.t('language_label'), exact: true });
	for (const screen of SCREENS) {
		await expect(
			group.getByRole('button', { name: screen.toUpperCase(), exact: true })
		).toHaveAttribute('aria-pressed', String(screen === locale));
	}
	if (locale !== 'es') await expectNoSpanish([player], locale, where);
}

/**
 * The first visit follows the browser language (`nl*` is Dutch, `es*` Spanish, `en*` English, anything
 * else Spanish). The context's `locale` is what the page reads as `navigator.language` and sends as
 * `Accept-Language`.
 */
const FIRST_VISIT: { browser: string; screen: Locale }[] = [
	{ browser: 'nl-NL', screen: 'nl' },
	{ browser: 'nl-BE', screen: 'nl' },
	{ browser: 'en-GB', screen: 'en' },
	{ browser: 'es-ES', screen: 'es' },
	{ browser: 'fr-FR', screen: 'es' }
];

for (const { browser, screen } of FIRST_VISIT) {
	test(`a first visit with a ${browser} browser shows the Home in ${screen}`, async ({ app }) => {
		const ana = await app.player('Ana', { locale: screen, browserLanguage: browser });
		await ana.home.open();
		await expectHomeIn(ana, `home (${browser})`);
	});
}

test('the language chosen in the switch beats the browser language and survives a reload', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'nl', browserLanguage: 'nl-NL' });
	await ana.home.open();
	await expectHomeIn(ana, 'first visit');

	for (const choice of ['en', 'es', 'nl'] as const) {
		await ana.home.switchLanguage(choice);
		await expectHomeIn(ana, `after choosing ${choice}`);
		await ana.reload();
		await expectHomeIn(ana, `reloaded after choosing ${choice}`);
	}
});

test('the choice is kept by the browser: a new tab and a later visit start in that language', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'en', browserLanguage: 'en-GB' });
	await ana.home.open();
	await ana.home.switchLanguage('nl');
	await expectHomeIn(ana, 'chosen nl');

	const tab = await ana.newTab();
	await tab.goto('/');
	await expect(tab.locator('html')).toHaveAttribute('lang', 'nl');
	await expect(tab.getByLabel(ana.t('nickname_label'), { exact: true })).toBeVisible();
	await tab.close();

	await ana.page.goto('/');
	await expectHomeIn(ana, 'visited again');
});
