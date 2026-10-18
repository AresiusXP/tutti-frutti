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

test.describe.configure({ mode: 'parallel' });

const categories = CLASSIC_CATEGORIES.es;

test('reloading the page mid-round brings the same round back, with the typed answers', async ({
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

	const typed = [
		answerFor(letter, 'Ana', 0),
		answerFor(letter, 'Ana', 1),
		answerFor(letter, 'Ana', 2)
	];
	await ana.playing.fillAll([...typed, null, null, null]);
	await expectServerHas(beto, ana, 3, 6); // the server has them: that is what comes back

	await test.step('Ana reloads: same room, same round, same letter, answers restored', async () => {
		await ana.reload();
		await ana.expectScreen('playing');
		expect(await ana.playing.roundInfo()).toEqual({ round, letter });
		expect(ana.code).toBe(code);
		await ana.playing.expectValues([...typed, '', '', '']);
		await expect(ana.reconnectingBanner).toHaveCount(0);
		await expect(beto.playing.progress('Ana', 3, 6)).toBeAttached();
	});

	await test.step('the round carries on: Ana finishes her fields and calls Basta', async () => {
		await ana.playing.fillAll([null, null, null, ...uniqueAnswers(letter, 'Ana', 6).slice(3)]);
		await beto.playing.fillAll(uniqueAnswers(letter, beto, 6));
		await bastaBy(ana, [beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
	});

	await test.step('a reload in the review keeps the category the host is on', async () => {
		await ana.review.next.click();
		await ana.review.next.click();
		await beto.review.expectCategory(categories[2] as string, 3, 6);
		await beto.reload();
		await beto.expectScreen('review');
		await beto.review.expectCategory(categories[2] as string, 3, 6);
		await beto.review.expectAnswer('Ana', answerFor(letter, 'Ana', 2));
		await beto.review.expectAnswer('Beto', answerFor(letter, 'Beto', 2));
	});

	await test.step('a reload on the summary shows the same totals', async () => {
		await ana.review.finish({ anyway: true });
		await beto.summary.expectTotals({ Ana: 60, Beto: 60 });
		await beto.reload();
		await beto.expectScreen('summary');
		await beto.summary.expectTotals({ Ana: 60, Beto: 60 });
	});
});

test('losing the connection mid-round: banner, answers typed offline arrive after the reconnect', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto', { network: true });
	const everyone = [ana, beto];
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, everyone);
	await chooserStops(everyone);
	const { letter } = await ana.playing.roundInfo();
	const network = beto.network;
	if (!network) throw new Error('Beto has no network control');

	await ana.playing.fillAll(uniqueAnswers(letter, ana, 6));
	const offlineAnswers = uniqueAnswers(letter, beto, 6);

	await test.step('the network goes away: Beto gets the banner, Ana sees Beto offline', async () => {
		await network.drop();
		await expect(beto.offlineBanner).toBeVisible();
		await expect(
			ana.page.getByText(ana.t('play_progress_offline', { name: 'Beto', filled: 0, total: 6 }), {
				exact: true
			})
		).toBeAttached();
	});

	await test.step('Beto keeps typing offline; Basta waits for the connection', async () => {
		await beto.playing.fillAll(offlineAnswers);
		await expect(beto.playing.bastaButton).toBeDisabled();
		await expect(beto.page.getByText(beto.t('play_hint_offline'), { exact: true })).toBeVisible();
		// nothing reached the server meanwhile
		await expect(
			ana.page.getByText(ana.t('play_progress_offline', { name: 'Beto', filled: 0, total: 6 }), {
				exact: true
			})
		).toBeAttached();
	});

	await test.step('the network is back: reconnects, answers are delivered, Basta is enabled again', async () => {
		await network.restore();
		await expect(beto.offlineBanner).toHaveCount(0);
		await expect(beto.reconnectingBanner).toHaveCount(0);
		await expectServerHas(ana, beto, 6, 6);
		await expect(beto.playing.bastaButton).toBeEnabled();
		await expect(beto.page.getByText(beto.t('play_hint_ready'), { exact: true })).toBeVisible();
		await beto.playing.expectValues(offlineAnswers);
	});

	await test.step('the offline answers are what the review shows', async () => {
		await bastaBy(beto, [ana]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
		for (const p of everyone) {
			await p.review.expectAnswer('Beto', offlineAnswers[0] as string);
			await p.review.expectPoints('Beto', 10);
			await p.review.expectPoints('Ana', 10);
		}
		await ana.review.finish({ anyway: true });
		for (const p of everyone) await p.summary.expectTotals({ Ana: 60, Beto: 60 });
	});
});

test('a dropped connection in the lobby heals by itself: seat kept, no duplicates', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto', { network: true });
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await ana.lobby.expectPlayers(['Ana', 'Beto']);

	await beto.network?.drop();
	await ana.lobby.expectOffline('Beto');
	await ana.lobby.expectPlayers(['Ana', 'Beto']); // the seat is kept

	await beto.network?.restore();
	await ana.lobby.expectOffline('Beto', false);
	await beto.lobby.expectPlayers(['Ana', 'Beto']);
	await expect(beto.reconnectingBanner).toHaveCount(0);
});
