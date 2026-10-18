import { expect, test } from '../src/fixtures';
import {
	answerFor,
	chooserStops,
	createRoom,
	expectServerHas,
	joinByCode,
	startGame
} from '../src/flows';

/** "1:00" -> 60 */
const seconds = (clock: string) => {
	const [m, s] = clock.split(':').map(Number);
	return (m ?? 0) * 60 + (s ?? 0);
};

/**
 * The server owns the deadline, so this runs for real: the shortest allowed limit is 60 s
 * (off, 60, 90, 120, 180). Lives in the `slow` project so the default run stays fast.
 */
test('a round with a 60 s time limit ends by itself, nobody calls Basta', async ({ app }) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const everyone = [ana, beto];

	const code = await createRoom(app, ana);
	await joinByCode(beto, code);

	await test.step('the host picks 60 s from the allowed values; the guest sees it', async () => {
		const allowed = ana.page
			.getByRole('group', { name: ana.t('lobby_time_label'), exact: true })
			.getByRole('button');
		await expect(allowed).toHaveCount(5);
		await expect(allowed).toHaveText([
			ana.t('lobby_time_off'),
			...[60, 90, 120, 180].map((n) => ana.t('lobby_time_secs', { n }))
		]);
		await ana.lobby.setTimeLimit(60);
		await expect(beto.lobby.chips.nth(3)).toHaveText(beto.t('lobby_summary_time', { n: 60 }));
	});

	await startGame(ana, everyone);
	await chooserStops(everyone);
	const letter = await ana.playing.letter();

	await test.step('the clock shows the remaining time and counts down', async () => {
		for (const p of everyone) {
			await expect(p.playing.timer).toHaveText(/^(1:00|0:[45]\d)$/);
			expect(seconds((await p.playing.timer.textContent()) ?? '')).toBeLessThanOrEqual(60);
		}
		const first = seconds((await ana.playing.timer.textContent()) ?? '');
		await expect
			.poll(async () => seconds((await ana.playing.timer.textContent()) ?? ''), { timeout: 15_000 })
			.toBeLessThan(first);
	});

	await test.step('players write while the clock runs (no Basta: not every field is filled)', async () => {
		await ana.playing.fill('Nombre', answerFor(letter, 'Ana', 0));
		await beto.playing.fillAll([
			answerFor(letter, 'Beto', 0),
			answerFor(letter, 'Beto', 1),
			null,
			null,
			null,
			null
		]);
		await expectServerHas(ana, beto, 2, 6);
		await expectServerHas(beto, ana, 1, 6);
		// the button only offers a vote (it never calls Basta with fields missing): the hint counts what is missing
		await expect(ana.playing.hint(ana.t('play_hint_ask_many', { count: 5 }))).toBeVisible();
		await expect(beto.playing.hint(beto.t('play_hint_ask_many', { count: 4 }))).toBeVisible();
		await ana.playing.openAskDialog();
		await ana.playing.askKeep.click();
		await expect(ana.playing.askDialog).toBeHidden();
		expect(ana.sentCount('basta')).toBe(0);
		expect(ana.sentCount('ask_stop')).toBe(0);
		// the "time's up" overlay and the grace banner last under a second each: record, never race them
		await Promise.all(everyone.map((p) => p.playing.watchSplash()));
		await Promise.all(everyone.map((p) => p.playing.watchGrace()));
	});

	await test.step('the last seconds turn the clock urgent, then the room locks without a caller', async () => {
		for (const p of everyone) {
			await expect(p.page.getByText(p.t('play_time_warning'), { exact: true })).toBeAttached({
				timeout: 70_000
			});
		}
		for (const p of everyone) await p.expectScreen('review', { timeout: 30_000 });
		for (const p of everyone) {
			const grace = await p.playing.graces();
			const seen = grace.texts.join(' | ');
			expect(
				grace.texts.some((text) => text.includes(p.t('play_time_up'))),
				`the grace banner said the time was up (saw: ${seen})`
			).toBe(true);
			expect(
				grace.texts.some((text) => text.includes(p.t('play_locked'))),
				`the grace banner said the answers are locked (saw: ${seen})`
			).toBe(true);
			expect(
				grace.texts.some((text) => text.includes(p.t('play_caller_called', { name: 'Ana' }))),
				`nobody called Basta (saw: ${seen})`
			).toBe(false);
			expect(grace.inputLocked, 'the answer fields were locked while the banner was up').toBe(true);
		}
		await beto.playing.expectSplash([beto.t('play_time_up')]);
	});

	await test.step('the answers written before the deadline reach the review and the score', async () => {
		for (const p of everyone) await p.expectScreen('review', { timeout: 15_000 });
		for (const p of everyone) {
			await p.review.expectAnswer('Ana', answerFor(letter, 'Ana', 0));
			await p.review.expectAnswer('Beto', answerFor(letter, 'Beto', 0));
			await p.review.expectPoints('Ana', 10);
			await p.review.expectPoints('Beto', 10);
		}
		await ana.review.next.click();
		for (const p of everyone) {
			await p.review.expectCategory('País', 2, 6);
			await p.review.expectNoPoints('Ana'); // nothing written: no pill
			await p.review.expectPoints('Beto', 10);
		}
		await ana.review.finish({ anyway: true });
		for (const p of everyone) await p.summary.expectTotals({ Ana: 10, Beto: 20 });
	});
});
