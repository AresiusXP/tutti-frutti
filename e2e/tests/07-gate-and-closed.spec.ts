import { Bot, joinBots } from '../src/bots';
import { expect, test } from '../src/fixtures';
import {
	chooserStops,
	createRoom,
	joinByCode,
	joinByInviteLink,
	quickRound,
	startGame,
	uniqueAnswers
} from '../src/flows';
import { MAX_PLAYERS } from '../src/rules';

test.describe.configure({ mode: 'parallel' });

const NOT_FOUND = /\/api\/rooms\/[A-Z0-9]{4}$/;

test('an unknown room code: "no such room" on the invite link and a toast on Home', async ({
	app
}) => {
	const dani = await app.player('Dani');
	app.guard.allow({
		kind: 'response',
		url: NOT_FOUND,
		status: 404,
		why: 'looking up a room that does not exist'
	});

	await test.step('the invite link of a room that does not exist', async () => {
		await dani.page.goto('/W2W2');
		await dani.gate.expectBlocked('not_found');
		await expect(dani.gate.joinButton).toHaveCount(0);
		await dani.gate.homeLink.click();
		await expect(dani.home.createButton).toBeVisible();
		await expect(dani.page).toHaveURL(/\/$/);
	});

	await test.step('the same code typed on Home', async () => {
		await dani.home.setNickname('Dani');
		await dani.home.submitCode('W2W2');
		await expect(dani.home.toast(dani.t('err_room_not_found'))).toBeVisible();
		await expect(dani.page).toHaveURL(/\/$/); // stayed on Home
	});
});

test('a full lobby (10 players) turns newcomers away; a free seat lets them in', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const dani = await app.player('Dani');
	const code = await createRoom(app, ana);
	const names = Array.from({ length: MAX_PLAYERS - 1 }, (_, i) => `Bot${i + 1}`);
	const bots = await joinBots(code, names);
	app.onDispose(() => bots.forEach((b) => b.close()));

	await test.step('ten players: the lobby says so', async () => {
		await expect(ana.lobby.playerItems).toHaveCount(MAX_PLAYERS);
		await expect(
			ana.page.getByLabel(ana.t('lobby_players_count_label', { count: 10, max: 10 }), {
				exact: true
			})
		).toBeVisible();
	});

	await test.step('the invite link shows "the room is full"', async () => {
		await dani.page.goto(`/${code}`);
		await dani.gate.expectBlocked('full');
		await expect(dani.gate.joinButton).toHaveCount(0);
	});

	await test.step('typing the code on Home gives a toast', async () => {
		await dani.home.open();
		await dani.home.setNickname('Dani');
		await dani.home.submitCode(code);
		await expect(dani.home.toast(dani.t('home_room_full'))).toBeVisible();
	});

	await test.step('the host removes a bot (it is told 4002) and Dani takes the seat', async () => {
		await ana.lobby.kick('Bot9');
		await expect.poll(() => (bots[8] as Bot).closeCode).toBe(4002);
		await expect(ana.lobby.playerItems).toHaveCount(MAX_PLAYERS - 1);
		await joinByInviteLink(dani, code);
		await expect(ana.lobby.playerItems).toHaveCount(MAX_PLAYERS);
		await ana.lobby.expectPlayers(['Ana', ...names.slice(0, 8), 'Dani']);
	});
});

test('a kicked player lands on the closed screen and cannot walk back in', async ({ app }) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await ana.lobby.expectPlayers(['Ana', 'Beto']);

	await ana.lobby.kick('Beto');
	await beto.closed.expect('kicked', beto.t('closed_kicked_text'));
	await ana.lobby.expectPlayers(['Ana']);
	await beto.closed.homeLink.click();
	await expect(beto.home.createButton).toBeVisible();

	await test.step('the ban holds: through the invite link he is sent out again', async () => {
		await beto.page.goto(`/${code}`);
		await beto.gate.expectOpen(code);
		await beto.gate.join();
		await beto.closed.expect('kicked', beto.t('closed_kicked_text'));
		await ana.lobby.expectPlayers(['Ana']);
	});
});

test('somebody who joins mid-round waits for the next round, then plays it', async ({ app }) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const caro = await app.player('Caro');
	const players = [ana, beto];
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, players);

	await test.step('Caro arrives while the letter is being chosen: she is told to wait', async () => {
		await joinByInviteLink(caro, code, { landing: 'letter' });
		await expect(caro.letter.joinsNextRound).toBeVisible();
		await expect(caro.letter.stopButton).toHaveCount(0);
	});

	await test.step('the round starts without her: she watches, nobody waits for her answers', async () => {
		await chooserStops([ana, beto, caro]);
		await caro.expectScreen('playing');
		await expect(caro.playing.spectatorNote).toBeVisible();
		await expect(caro.playing.fields).toHaveCount(0);
		await expect(caro.playing.bastaButton).toHaveCount(0);
		const { letter } = await ana.playing.roundInfo();
		await ana.playing.fillAll(uniqueAnswers(letter, ana, 6));
		await expect(ana.playing.bastaButton).toBeEnabled();
		await ana.playing.bastaButton.click();
		await expect(caro.playing.graceBanner).toContainText(
			caro.t('play_caller_called', { name: 'Ana' })
		);
	});

	await test.step('in the review she can look but not vote', async () => {
		await caro.expectScreen('review');
		await expect(caro.review.spectatorNote).toBeVisible();
		await expect(caro.review.rejectButton('Ana')).toHaveCount(0);
		await ana.review.finish({ anyway: true });
	});

	await test.step('the summary lists her with 0 points and "joins next round"', async () => {
		await caro.expectScreen('summary');
		await caro.summary.expectTotals({ Ana: 60, Beto: 0, Caro: 0 });
		await expect(caro.summary.row('Caro')).toContainText(caro.t('summary_joins_next'));
	});

	await test.step('next round: Caro has her fields like everybody else', async () => {
		await ana.summary.nextRound();
		await Promise.all([ana, beto, caro].map((p) => p.expectScreen('letter')));
		await expect(caro.letter.joinsNextRound).toHaveCount(0);
		await chooserStops([ana, beto, caro]);
		for (const p of [ana, beto, caro]) await expect(p.playing.fields).toHaveCount(6);
		await expect(caro.playing.spectatorNote).toHaveCount(0);
	});
});

test('a second tab of the same player replaces the first, which can take its seat back', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const code = await createRoom(app, ana);
	const first = ana.page;
	const second = await ana.newTab();

	await test.step('the invite link in a second tab joins as the same player', async () => {
		await second.goto(`/${code}`);
		await second.getByRole('button', { name: ana.t('gate_join'), exact: true }).click();
		await expect(
			second.getByRole('main', { name: ana.t('lobby_screen_label'), exact: true })
		).toBeVisible();
	});

	await test.step('the first tab is told it was replaced', async () => {
		await expect(
			first.getByRole('heading', { name: ana.t('closed_replaced_title'), exact: true })
		).toBeVisible();
		await expect(first.getByText(ana.t('closed_replaced_text'), { exact: true })).toBeVisible();
	});

	await test.step('"take my seat back" swaps them: the second tab is now the replaced one', async () => {
		await first.getByRole('button', { name: ana.t('closed_replaced_action'), exact: true }).click();
		await expect(
			first.getByRole('main', { name: ana.t('lobby_screen_label'), exact: true })
		).toBeVisible();
		await expect(
			second.getByRole('heading', { name: ana.t('closed_replaced_title'), exact: true })
		).toBeVisible();
	});
});

test('a finished game cannot be joined: "the game is over" on the link and on Home', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const dani = await app.player('Dani');
	const code = await createRoom(app, ana);
	await joinByCode(beto, code);
	await startGame(ana, [ana, beto]);
	await quickRound({ host: ana, everyone: [ana, beto], caller: beto, fields: 6 });
	await ana.summary.endGame();
	await ana.expectScreen('final');

	await dani.page.goto(`/${code}`);
	await dani.gate.expectBlocked('over');
	await dani.home.open();
	await dani.home.setNickname('Dani');
	await dani.home.submitCode(code);
	await expect(dani.home.toast(dani.t('home_game_over'))).toBeVisible();
});
