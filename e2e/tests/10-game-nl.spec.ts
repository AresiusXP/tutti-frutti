import { expect, test } from '../src/fixtures';
import {
	bastaBy,
	chooserStopsAfter,
	createRoom,
	expectServerHas,
	joinByCode,
	playOutRound
} from '../src/flows';
import { expectDutchOnly } from '../src/no-spanish';
import { playReferenceGame } from '../src/scenarios/reference-game';
import { CLASSIC_CATEGORIES, LETTER_TICK_MS, playableLetters } from '../src/rules';

test.describe.configure({ mode: 'parallel' });

test('the same game in Dutch says "Stop!", reads ĳ as ij and shows no Spanish anywhere', async ({
	app
}) => {
	await playReferenceGame(app, 'nl', {
		onScreen: (players, phase) => expectDutchOnly(players, phase)
	});
});

/**
 * The Dutch ligature ĳ / Ĳ (one character) is "ij" everywhere, so "Ĳsland" == "IJsland" == "ijsland" and
 * an answer that starts with it starts with the letter I (never with Y). The letter has to be I for
 * that, and the server picks it from the time between the start of the round and the chooser's tap
 * (`letters[floor(elapsed / 120 ms) mod n]`), so the chooser taps at the middle of I's 120 ms slot
 * (timed inside the page); a miss plays a quick round, corrects the aim by what the revealed letter
 * says and tries again.
 */
const MAX_ROUNDS = 8;

test('answers written with the ĳ ligature count for the letter I: "Ĳsland" is "IJsland"', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'nl' });
	const beto = await app.player('Beto', { locale: 'nl' });
	const everyone = [ana, beto];
	const categories = CLASSIC_CATEGORIES.nl;
	const [naam, land, dier, eten, kleur, merk] = categories as [
		string,
		string,
		string,
		string,
		string,
		string
	];

	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await ana.lobby.setRounds(MAX_ROUNDS);

	const played: string[] = [];
	let missedRounds = 0;
	let offsetMs = -10; // the page sees the screen a few ms after the server started the round

	await test.step('the chooser taps in the slot of the letter I', async () => {
		for (let round = 1; ; round++) {
			expect(
				round,
				`no letter I in ${MAX_ROUNDS} rounds (letters: ${played.join(' ')})`
			).toBeLessThanOrEqual(MAX_ROUNDS);
			const letters = playableLetters('nl', true).filter((l) => !played.includes(l));
			const slot = letters.indexOf('I');
			let aimMs = slot * LETTER_TICK_MS + LETTER_TICK_MS / 2 + offsetMs;
			while (aimMs < 700) aimMs += letters.length * LETTER_TICK_MS; // time to find the chooser

			await Promise.all(everyone.map((p) => p.letter.watchArrival()));
			if (round === 1) await ana.lobby.start();
			else await ana.summary.nextRound();
			await chooserStopsAfter(everyone, aimMs);

			const { letter } = await ana.playing.roundInfo();
			played.push(letter);
			if (letter === 'I') break;

			// a miss: how many slots off (the list is circular), then play the round out and aim again
			const half = letters.length / 2;
			let off = letters.indexOf(letter) - slot;
			if (off > half) off -= letters.length;
			if (off < -half) off += letters.length;
			offsetMs -= off * LETTER_TICK_MS;
			await playOutRound({ host: ana, everyone, caller: beto, fields: 6 });
			missedRounds++;
		}
		test
			.info()
			.annotations.push(
				{ type: 'letters', description: played.join(' ') },
				{ type: 'aim-offset', description: `${offsetMs} ms` }
			);
	});

	const anaAnswers = ['Ilse', 'Ĳsland', 'Ĳsvogel', 'Ĳs', 'Indigo', 'Ikea'];
	const betoAnswers = ['Iris', 'IJsland', 'ĳsvogel', 'Ijs', 'Ĳsblauw', 'Iglo'];
	const points = [10, 5, 5, 5, 10, 10];

	await test.step('"Ysland" is not an I, "Ĳsland" and "IJsland" are', async () => {
		await beto.playing.fill(land, 'Ysland');
		await expect(beto.playing.field(land)).toHaveAttribute('aria-invalid', 'true');
		await expect(
			beto.page.getByText(beto.t('play_field_wrong_letter', { letter: 'I' }))
		).toBeVisible();
		// the wrong answer and the empty fields keep him from calling Stop: the button offers a vote instead
		await expect(
			beto.playing.hint(beto.t('play_hint_ask_wrong', { categories: land }))
		).toBeVisible();
		await beto.playing.openAskDialog();
		await expect(
			beto.playing.askDialog.getByText(beto.t('play_ask_wrong_one', { letter: 'I' }), {
				exact: true
			})
		).toBeVisible();
		await beto.playing.askKeep.click();
		await expect(beto.playing.askDialog).toBeHidden();
		expect(beto.sentCount('basta'), 'no Stop! with a wrong answer').toBe(0);
		expect(beto.sentCount('ask_stop'), 'nothing was asked').toBe(0);

		await ana.playing.fillAll(anaAnswers);
		await beto.playing.fillAll(betoAnswers); // overwrites "Ysland"
		for (const p of everyone) {
			await expect(p.playing.field(land)).not.toHaveAttribute('aria-invalid', 'true');
			await expect(p.page.getByText(p.t('play_field_valid'), { exact: true })).toHaveCount(6);
		}
		await expectServerHas(ana, beto, 6, 6);
		await expectServerHas(beto, ana, 6, 6);
		await expectDutchOnly(everyone, 'letter I playing');
	});

	await test.step('review: each answer as typed, "ij" in any spelling is one answer worth 5', async () => {
		await bastaBy(ana, [beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
		for (const [i, label] of [naam, land, dier, eten, kleur, merk].entries()) {
			if (i > 0) await ana.review.next.click();
			for (const p of everyone) {
				await p.review.expectCategory(label, i + 1, 6);
				await p.review.expectAnswer('Ana', anaAnswers[i] as string);
				await p.review.expectAnswer('Beto', betoAnswers[i] as string);
				await p.review.expectPoints('Ana', points[i] as number);
				await p.review.expectPoints('Beto', points[i] as number);
			}
			if (points[i] === 5) {
				for (const name of ['Ana', 'Beto']) {
					await expect(ana.review.card(name)).toContainText(ana.t('review_shared_one'));
				}
			}
		}
		await expectDutchOnly(everyone, 'letter I review');
	});

	await test.step('summary: 45 points each this round', async () => {
		await ana.review.finish();
		for (const p of everyone) {
			await p.expectScreen('summary');
			await p.summary.expectRoundPoints({ Ana: 45, Beto: 45 });
			await p.summary.expectTotals({ Ana: 45, Beto: 45 + 60 * missedRounds });
		}
	});
});
