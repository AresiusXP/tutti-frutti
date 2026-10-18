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
		await expect(ana.playing.bastaButton).toBeDisabled();
		await Promise.all(everyone.map((p) => p.playing.watchSplash())); // the "time's up" overlay lasts under a second
	});

	await test.step('the last seconds turn the clock urgent, then the room locks without a caller', async () => {
		for (const p of everyone) {
			await expect(p.page.getByText(p.t('play_time_warning'), { exact: true })).toBeAttached({
				timeout: 70_000
			});
		}
		for (const p of everyone) {
			await expect(p.playing.graceBanner).toContainText(p.t('play_time_up'), { timeout: 30_000 });
			await expect(p.playing.graceBanner).not.toContainText(
				p.t('play_caller_called', { name: 'Ana' })
			);
		}
		await beto.playing.expectSplash([beto.t('play_time_up')]);
		await expect(ana.playing.graceBanner).toContainText(ana.t('play_locked'), { timeout: 10_000 });
		await expect(ana.playing.field('Nombre')).toBeDisabled();
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
