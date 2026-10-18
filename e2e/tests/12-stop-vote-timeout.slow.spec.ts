import { expect, test } from '../src/fixtures';
import { answersLeaving, askToStop, expectServerHas, fillAnswers, openRound } from '../src/flows';
import type { Locale } from '../src/messages';
import { expectNoSpanish } from '../src/no-spanish';
import { STOP_VOTE_SECONDS } from '../src/rules';

test.describe.configure({ mode: 'parallel' });

/**
 * The server owns the 15 s of a vote to stop (`StopVoteDuration`, no setting or variable shortens it), so
 * this waits for real, once per screen language. Lives in the `slow` project like the 60 s time limit.
 */
for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`15 seconds without everybody's yes reject the vote (${locale})`, async ({ app }) => {
		const {
			players: [ana, ben, cleo],
			letter: L
		} = await openRound(app, { locale });
		if (!ana || !ben || !cleo) throw new Error('three players expected');
		const everyone = [ana, ben, cleo];
		await Promise.all([
			fillAnswers(ana, answersLeaving(L, ana, [5])),
			fillAnswers(ben, answersLeaving(L, ben, [4])),
			fillAnswers(cleo, answersLeaving(L, cleo))
		]);
		await expectServerHas(cleo, ana, 5, 6);

		await askToStop(ana, [ben, cleo]);
		const asked = Date.now();
		const first = await ana.playing.voteSecondsLeft();
		expect(first).toBeLessThanOrEqual(STOP_VOTE_SECONDS);
		expect(first).toBeGreaterThan(STOP_VOTE_SECONDS - 5);

		await test.step('Ben says yes, Cleo says nothing: one yes is not enough', async () => {
			await ben.playing.voteYes.click();
			await ana.playing.expectTally(1, 2);
			await expect
				.poll(() => ana.playing.voteSecondsLeft(), { message: 'the countdown runs' })
				.toBeLessThan(first);
			if (locale !== 'es') await expectNoSpanish(everyone, locale, 'vote open');
		});

		await test.step('the clock runs out: the asker is told, the prompts disappear, the round goes on', async () => {
			await expect(ana.playing.toast(ana.t('play_stop_timeout'))).toBeVisible({ timeout: 25_000 });
			const waited = Date.now() - asked;
			expect(waited, 'about 15 s').toBeGreaterThan(13_000);
			expect(waited, 'about 15 s').toBeLessThan(25_000);
			for (const p of everyone) await p.playing.expectNoVote();
			for (const voter of [ben, cleo]) {
				await expect(
					voter.playing.toast(voter.t('play_stop_timeout')),
					'only the asker'
				).toHaveCount(0);
			}
			for (const p of everyone) {
				await p.expectScreen('playing');
				await expect(p.playing.graceBanner).toHaveCount(0);
			}
			await expect(ana.playing.bastaButton).toBeEnabled(); // no cooldown: she may ask again
			if (locale !== 'es') await expectNoSpanish(everyone, locale, 'vote timed out');
		});
	});
}
