import { expect, test } from '../src/fixtures';
import {
	bastaBy,
	createRoom,
	fillAnswers,
	joinByCode,
	openRound,
	startGame,
	uniqueAnswers
} from '../src/flows';

test.describe.configure({ mode: 'parallel' });

/**
 * Reporting (docs/DESIGN.md, "Content safety"): a flag next to what another player wrote, a form with the
 * fixed reasons, and a report that is only called sent once the server stored it (201).
 */
const REPORTS = /\/api\/reports$/;
const KEY = /^[A-Za-z0-9_-]{8,64}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

test('two players: reporting a nickname in the lobby and an answer in the review, each stored (201)', async ({
	app
}) => {
	const { players, code } = await openRound(app, { names: ['Ana', 'Ben'] });
	const [ana, ben] = players as [(typeof players)[0], (typeof players)[0]];
	const { letter } = await ana.playing.roundInfo();
	await fillAnswers(ana, uniqueAnswers(letter, ana, 6));
	await fillAnswers(ben, uniqueAnswers(letter, ben, 6));
	await bastaBy(ana, [ben]);
	await Promise.all(players.map((p) => p.expectScreen('review')));

	await test.step('only the OTHER player has a flag, and it keeps a 48 px hit area', async () => {
		await expect(ana.safety.flagAnswer('Ben')).toBeVisible();
		await expect(ana.safety.flagAnswer('Ana')).toHaveCount(0);
		const box = (await ana.safety.flagAnswer('Ben').boundingBox())!;
		expect(Math.min(box.width, box.height)).toBeGreaterThanOrEqual(47.5);
	});

	await test.step("Ana reports Ben's answer: round, category and author, quoted as she saw it", async () => {
		await ana.safety.flagAnswer('Ben').click();
		await expect(ana.safety.dialog(ana.t('report_menu_title', { name: 'Ben' }))).toBeVisible();
		await ana.safety.menuItem('report_menu_answer').click();
		// the form: nothing is sent until a reason is chosen
		await ana.safety.sendButton.click();
		await expect(ana.safety.alert).toHaveText(ana.t('report_reason_required'));
		await ana.safety.reason('spam').check();
		await ana.safety.comment.fill('not a real answer');
		const { body, response } = await ana.safety.submit(ana.safety.sendButton);
		expect(response.status()).toBe(201);
		expect(body).toMatchObject({
			kind: 'answer',
			code,
			reason: 'spam',
			comment: 'not a real answer',
			ref: { round: 1, category: 0 },
			observedText: uniqueAnswers(letter, ben, 6)[0]
		});
		expect(body.roomInstanceId).toMatch(UUID);
		expect(body.idempotencyKey).toMatch(KEY);
		// the player id is the author's guest id (opaque); the client never sends anything but ids it was shown
		expect(body.ref.playerId).toBeTruthy();
		expect(Object.keys(body).sort()).toEqual(
			[
				'code',
				'comment',
				'idempotencyKey',
				'kind',
				'observedText',
				'reason',
				'ref',
				'roomInstanceId'
			].sort()
		);
		expect(((await response.json()) as { id: string }).id).toMatch(UUID);
		// "sent" only now: toast, sheet closed
		await expect(ana.safety.sentToast).toBeVisible();
		await expect(ana.safety.sheet).toHaveCount(0);
	});

	await test.step("the same flag also reports Ben's nickname or avatar (kind player)", async () => {
		await ana.safety.flagAnswer('Ben').click();
		await ana.safety.menuItem('report_menu_player').click();
		await ana.safety.reason('impersonation').check();
		const { body, response } = await ana.safety.submit(ana.safety.sendButton);
		expect(response.status()).toBe(201);
		expect(body).toMatchObject({
			kind: 'player',
			code,
			reason: 'impersonation',
			observedText: 'Ben'
		});
		expect(body).not.toHaveProperty('comment');
	});

	await test.step('reporting never needs the terms and never touches the game: Ben is still there and nothing was sent on the socket', async () => {
		await expect(ben.review.main).toBeVisible();
		expect(ana.sentCount('leave')).toBe(0);
	});
});

test('the lobby flag reports a nickname, and the report is only "sent" after the 201', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const ben = await app.player('Ben');
	const code = await createRoom(app, ana);
	await joinByCode(ben, code);
	await ana.lobby.expectPlayers(['Ana', 'Ben']);

	await expect(ana.safety.flagPlayer('Ana')).toHaveCount(0);
	await ana.safety.flagPlayer('Ben').click();
	await ana.safety.menuItem('report_menu_player').click();
	await ana.safety.reason('harassment').check();
	const { body, response } = await ana.safety.submit(ana.safety.sendButton);
	expect(response.status()).toBe(201);
	expect(body).toMatchObject({ kind: 'player', code, observedText: 'Ben' });
	await expect(ana.safety.sentToast).toBeVisible();
	await ana.lobby.expectPlayers(['Ana', 'Ben']);
	await startGame(ana, [ana, ben]);
});

test('a failed report keeps the draft, says why, and the retry sends the SAME key (one report)', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const ben = await app.player('Ben');
	const code = await createRoom(app, ana);
	await joinByCode(ben, code);
	await ana.lobby.expectPlayers(['Ana', 'Ben']);

	const bodies: { idempotencyKey: string; comment?: string; reason: string }[] = [];
	let attempts = 0;
	await ana.page.route(REPORTS, async (route) => {
		bodies.push(route.request().postDataJSON() as never);
		if (attempts++ === 0) return route.abort('failed');
		return route.continue();
	});
	app.guard.allow({
		kind: 'failed',
		url: REPORTS,
		why: 'the first send of the report is cut on purpose (route.abort)'
	});

	await ana.safety.flagPlayer('Ben').click();
	await ana.safety.menuItem('report_menu_player').click();
	await ana.safety.reason('hate').check();
	await ana.safety.comment.fill('keep this text');
	await ana.safety.sendButton.click();

	await test.step('nothing is claimed: the error is shown, the sheet and the draft stay', async () => {
		await expect(ana.safety.alert).toContainText(ana.t('err_network'));
		await expect(ana.safety.alert).toContainText(
			ana.t('report_failed', { reason: ana.t('err_network') }).slice(0, 24)
		);
		await expect(ana.safety.sentToast).toHaveCount(0);
		await expect(ana.safety.reason('hate')).toBeChecked();
		await expect(ana.safety.comment).toHaveValue('keep this text');
		await expect(ana.safety.retryButton).toBeVisible();
	});

	await test.step('the retry goes through with the same idempotency key', async () => {
		const answered = ana.page.waitForResponse((r) => REPORTS.test(r.url()));
		await ana.safety.retryButton.click();
		expect((await answered).status()).toBe(201);
		await expect(ana.safety.sentToast).toBeVisible();
		await expect(ana.safety.sheet).toHaveCount(0);
	});

	expect(bodies).toHaveLength(2);
	const [first, second] = bodies;
	expect(second?.idempotencyKey).toBe(first?.idempotencyKey);
	expect(second).toMatchObject({ reason: 'hate', comment: 'keep this text' });
});

test('the server asking to slow down (429) is explained and nothing is claimed', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const ben = await app.player('Ben');
	const code = await createRoom(app, ana);
	await joinByCode(ben, code);
	await ana.lobby.expectPlayers(['Ana', 'Ben']);
	await ana.page.route(REPORTS, (route) =>
		route.fulfill({
			status: 429,
			headers: { 'Retry-After': '20' },
			json: { error: 'rate_limited' }
		})
	);
	app.guard.allow({
		kind: 'response',
		url: REPORTS,
		status: 429,
		why: 'the report endpoint is faked to answer 429'
	});
	await ana.safety.flagPlayer('Ben').click();
	await ana.safety.menuItem('report_menu_player').click();
	await ana.safety.reason('spam').check();
	await ana.safety.sendButton.click();
	await expect(ana.safety.alert).toContainText(ana.t('err_rate_limited'));
	await expect(ana.safety.sentToast).toHaveCount(0);
	await expect(ana.safety.sheet).toBeVisible();
});
