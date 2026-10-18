import { allowServerOutage, restartServer, waitForApi } from '../src/compose';
import { COMPOSE_PROJECT } from '../src/env';
import { expect, test } from '../src/fixtures';
import {
	answerFor,
	bastaBy,
	chooserStops,
	createRoom,
	expectServerHas,
	joinByCode,
	startGame,
	uniqueAnswers
} from '../src/flows';
import { CLASSIC_CATEGORIES } from '../src/rules';

// eslint-disable-next-line playwright/no-skipped-test -- only runnable against a compose stack
test.skip(
	!COMPOSE_PROJECT,
	'E2E_COMPOSE_PROJECT is not set: the stack cannot be restarted from here'
);

const categories = CLASSIC_CATEGORIES.es;

test('a server restart mid-game: both players come back to the same room, round and seats', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const everyone = [ana, beto];
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, everyone);
	await chooserStops(everyone);
	const { letter, round } = await ana.playing.roundInfo();

	const anaTyped = uniqueAnswers(letter, 'Ana', 2);
	const betoTyped = uniqueAnswers(letter, 'Beto', 3);
	await ana.playing.fillAll([...anaTyped, null, null, null, null]);
	await beto.playing.fillAll([...betoTyped, null, null, null]);
	await expectServerHas(ana, beto, 3, 6);
	await expectServerHas(beto, ana, 2, 6);

	const restartAndWait = async () => {
		const endOutage = allowServerOutage(app.guard);
		const restarted = restartServer();
		for (const p of everyone) await expect(p.reconnectingBanner).toBeVisible({ timeout: 30_000 });
		await restarted;
		await waitForApi();
		for (const p of everyone) await expect(p.reconnectingBanner).toBeHidden({ timeout: 60_000 });
		endOutage();
	};

	await test.step('the server restarts during the round: banner, then everybody is back', async () => {
		await restartAndWait();
		for (const p of everyone) {
			await p.expectScreen('playing');
			expect(await p.playing.roundInfo()).toEqual({ round, letter });
			expect(p.code).toBe(code);
		}
	});

	await test.step('same seats, same answers: nothing typed before the restart was lost', async () => {
		await ana.playing.expectValues([...anaTyped, '', '', '', '']);
		await beto.playing.expectValues([...betoTyped, '', '', '']);
		await expect(ana.playing.progress('Beto', 3, 6)).toBeAttached();
		await expect(beto.playing.progress('Ana', 2, 6)).toBeAttached();
	});

	await test.step('the round goes on as if nothing happened', async () => {
		await ana.playing.fillAll([null, null, ...uniqueAnswers(letter, 'Ana', 6).slice(2)]);
		await beto.playing.fillAll([null, null, null, ...uniqueAnswers(letter, 'Beto', 6).slice(3)]);
		await expectServerHas(ana, beto, 6, 6);
		await bastaBy(ana, [beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
	});

	await test.step('a second restart during the review keeps the host and the category', async () => {
		await ana.review.next.click();
		await ana.review.next.click();
		await beto.review.expectCategory(categories[2] as string, 3, 6);
		await restartAndWait();
		for (const p of everyone) {
			await p.expectScreen('review');
			await p.review.expectCategory(categories[2] as string, 3, 6);
			await p.review.expectAnswer('Ana', answerFor(letter, 'Ana', 2));
			await p.review.expectAnswer('Beto', answerFor(letter, 'Beto', 2));
		}
		await expect(ana.review.finishButton).toBeVisible(); // Ana is still the host
		await expect(beto.review.hostControls('Ana')).toBeVisible();
	});

	await test.step('the scores are the same as without the restarts', async () => {
		await ana.review.finish({ anyway: true });
		for (const p of everyone) await p.summary.expectTotals({ Ana: 60, Beto: 60 });
	});
});
