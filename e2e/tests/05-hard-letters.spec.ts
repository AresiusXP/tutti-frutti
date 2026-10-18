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

	await test.step('Dutch lobby: 26 letters, Q X Y skipped (23), rounds up to 23, Dutch category names', async () => {
		await ana.lobby.setLang('nl');
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('nl', true));
		await expect(ana.lobby.roundsMax(23)).toBeVisible();
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.nl);
		await expect(beto.lobby.chips.nth(0)).toHaveText(beto.t('lobby_lang_nl'));
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip('nl', true));
		await ana.lobby.setHardLetters(false);
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('nl', false));
		await expect(ana.lobby.roundsMax(26)).toBeVisible();
		await ana.lobby.setHardLetters(true);
	});

	await test.step('back to Spanish: its list applies again', async () => {
		await ana.lobby.setLang('es');
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow('es', true));
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.es);
	});
});

test('the Dutch lobby (Dutch screen): 26 letters, Q X Y are the hard ones, 23 are played', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'nl' });
	const beto = await app.player('Beto', { locale: 'nl' });
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);

	const hardRow = (on: boolean) =>
		on
			? ana.t('lobby_hard_on', { letters: HARD_LETTERS.nl.join(' '), count: 23 })
			: ana.t('lobby_hard_off', { count: 26 });
	const guestChip = (on: boolean) =>
		beto.t(on ? 'lobby_summary_hard_on' : 'lobby_summary_hard_off', { count: on ? 23 : 26 });

	await test.step('a room made from a Dutch screen is a Dutch lobby (Klassiekers)', async () => {
		const group = ana.lobby.settings.getByRole('group', {
			name: ana.t('lobby_lang_label'),
			exact: true
		});
		for (const lang of ['es', 'en', 'nl'] as const) {
			await expect(
				group.getByRole('button', { name: ana.t(`lobby_lang_${lang}`), exact: true })
			).toHaveAttribute('aria-pressed', String(lang === 'nl'));
		}
		await expect(beto.lobby.chips.nth(0)).toHaveText(beto.t('lobby_lang_nl'));
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.nl);
		await beto.lobby.expectCategories(CLASSIC_CATEGORIES.nl);
	});

	await test.step('hard letters out (the default): Q X Y are skipped, 23 letters', async () => {
		await expect(ana.lobby.hardLetters).toBeChecked();
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow(true));
		expect(hardRow(true)).toBe('Eruit: Q X Y · 23 letters in het spel');
		expect(playableLetters('nl', true)).toHaveLength(23);
		await expect(ana.lobby.roundsMax(23)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip(true));
	});

	await test.step('all letters in play: the whole alphabet, 26 letters (no Ñ)', async () => {
		await ana.lobby.setHardLetters(false);
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow(false));
		expect(hardRow(false)).toBe('Alle 26 letters in het spel');
		expect(ALPHABET.nl).toHaveLength(26);
		expect(ALPHABET.nl).not.toContain('Ñ');
		await expect(ana.lobby.roundsMax(26)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip(false));
	});

	await test.step('rounds go up to 26 and shrink to 23 when the hard letters go out again', async () => {
		await ana.lobby.setRounds(26);
		await expect(
			ana.page.getByRole('button', { name: ana.t('lobby_rounds_more'), exact: true })
		).toBeDisabled();
		await expect(beto.lobby.chips.nth(2)).toHaveText(beto.t('lobby_summary_rounds', { n: 26 }));
		await ana.lobby.setHardLetters(true);
		await expect(ana.lobby.roundsMax(23)).toBeVisible();
		await expect(ana.lobby.roundsValue(23)).toBeVisible();
		await expect(beto.lobby.chips.nth(1)).toHaveText(guestChip(true));
		await expect(beto.lobby.chips.nth(2)).toHaveText(beto.t('lobby_summary_rounds', { n: 23 }));
	});

	await test.step('the other languages keep their own lists, and Dutch comes back with its own', async () => {
		await ana.lobby.setLang('es');
		await expect(ana.lobby.hardLettersDescription).toHaveText(
			ana.t('lobby_hard_on', { letters: HARD_LETTERS.es.join(' '), count: 20 })
		);
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.es);
		await ana.lobby.setLang('en');
		await expect(ana.lobby.hardLettersDescription).toHaveText(
			ana.t('lobby_hard_on', { letters: HARD_LETTERS.en.join(' '), count: 20 })
		);
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.en);
		await ana.lobby.setLang('nl');
		await expect(ana.lobby.hardLettersDescription).toHaveText(hardRow(true));
		await ana.lobby.expectCategories(CLASSIC_CATEGORIES.nl);
	});
});

/**
 * Several quick rounds. The server picks `letters[floor(elapsed / 120 ms) mod n]`, so a tap right away
 * always yields the first letters: the chooser waits a random time (seeded: `E2E_SEED`) to sample the
 * whole list. Never a hard letter, never a letter twice, and the letters already played are shown.
 */
for (const locale of ['es', 'en', 'nl'] as Locale[]) {
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
