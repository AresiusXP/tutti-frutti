import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode, quickRound, startGame } from '../src/flows';
import type { Locale } from '../src/messages';
import { seeded } from '../src/random';
import {
	ALPHABET,
	CLASSIC_CATEGORIES,
	HARD_LETTERS,
	playableLetters,
	type Lang
} from '../src/rules';

test.describe.configure({ mode: 'parallel' });

test('"skip hard letters" and the game language drive the letter list and the rounds limit', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);

	const hardRow = (lang: Lang, on: boolean) =>
		on
			? ana.t('lobby_hard_on', {
					letters: HARD_LETTERS[lang].join(' '),
					count: playableLetters(lang, true).length
				})
			: ana.t('lobby_hard_off', { count: playableLetters(lang, false).length });
	const guestChip = (lang: Lang, on: boolean) =>
		on
			? beto.t('lobby_summary_hard_on', { count: playableLetters(lang, true).length })
			: beto.t('lobby_summary_hard_off', { count: playableLetters(lang, false).length });

	await test.step('Spanish, hard letters out (the default): K Ñ Q W X Y Z are skipped, 20 letters', async () => {
		await expect(ana.lobby.hardLetters).toBeChecked();
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('es', true));
		expect(hardRow('es', true)).toBe('Fuera: K Ñ Q W X Y Z · se juegan 20 letras');
		await expect(ana.lobby.roundsMax(20)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip('es', true));
	});

	await test.step('all letters in play: 27 in Spanish (Ñ after N)', async () => {
		await ana.lobby.setHardLetters(false);
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('es', false));
		await expect(ana.lobby.roundsMax(ALPHABET.es.length)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip('es', false));
	});

	await test.step('English lobby: 26 letters, its own hard list, English category names', async () => {
		await ana.lobby.setLang('en');
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('en', false));
		await expect(ana.lobby.roundsMax(26)).toBeVisible();
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.en);
		await expect(beto.lobby.chips.nth(0)).toHaveText(beto.t('lobby_lang_en'));
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip('en', false));
		await beto.lobby.expectCategories(CLASSIC_CATEGORIES.en);
	});

	await test.step('rounds go up to the number of letters in play (26)', async () => {
		await ana.lobby.setRounds(26);
		await expect(
			ana.page.getByRole('button', { name: ana.t('lobby_rounds_more'), exact: true })
		).toBeDisabled();
		await expect(beto.lobby.chips.nth(2)).toHaveText(beto.t('lobby_summary_rounds', { n: 26 }));
	});

	await test.step('hard letters out again: Q U V X Y Z are skipped and the rounds shrink to 20', async () => {
		await ana.lobby.setHardLetters(true);
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('en', true));
		expect(hardRow('en', true)).toBe('Fuera: Q U V X Y Z · se juegan 20 letras');
		await expect(ana.lobby.roundsMax(20)).toBeVisible();
		await expect(ana.lobby.roundsValue(20)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip('en', true));
		await expect(beto.lobby.chips.nth(2)).toHaveText(beto.t('lobby_summary_rounds', { n: 20 }));
	});

	await test.step('back to Spanish: its list applies again', async () => {
		await ana.lobby.setLang('es');
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('es', true));
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.es);
	});
});

/**
 * Several quick rounds. The server picks `letters[floor(elapsed / 120 ms) mod n]`, so a tap right away
 * always yields the first letters: the chooser waits a random time (seeded: `E2E_SEED`) to sample the
 * whole list. Never a hard letter, never a letter twice, and the letters already played are shown.
 */
for (const locale of ['es', 'en'] as Locale[]) {
	test(`the revealed letter is never a hard letter (${locale} lobby, several rounds)`, async ({
		app
	}) => {
		const rng = seeded();
		test.info().annotations.push({ type: 'seed', description: String(rng.seed) });
		const lang: Lang = locale;
		const ana = await app.player('Ana', { locale });
		const beto = await app.player('Beto', { locale });
		const everyone = [ana, beto];
		const code = await createRoom(app, ana);
		await joinByCode(beto, code);
		const rounds = 6;
		await ana.lobby.setRounds(rounds);
		await startGame(ana, everyone);

		const played: string[] = [];
		for (let round = 1; round <= rounds; round++) {
			if (round > 1) {
				await ana.summary.nextRound();
				await Promise.all(everyone.map((p) => p.expectScreen('letter')));
				await expect
					.poll(
						async () => (await ana.letter.usedLetters.allInnerTexts()).map((s) => s.trim()).sort(),
						{
							message: `round ${round}: the letters already played are listed`
						}
					)
					.toEqual([...played].sort());
			}
			const delayMs = Math.floor(rng.next() * playableLetters(lang, true).length * 120);
			const result = await quickRound({ host: ana, everyone, caller: beto, fields: 6, delayMs });
			expect(result.round).toBe(round);
			expect(ALPHABET[lang], `round ${round} letter`).toContain(result.letter);
			expect(
				HARD_LETTERS[lang],
				`round ${round} (tap after ${delayMs} ms): a hard letter came up`
			).not.toContain(result.letter);
			expect(played, 'letters are not repeated').not.toContain(result.letter);
			played.push(result.letter);
		}
		test.info().annotations.push({ type: 'letters', description: played.join(' ') });
		await expect(ana.summary.lastRoundNote).toBeVisible();
	});
}
