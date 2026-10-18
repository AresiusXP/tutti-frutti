import { Bot } from '../src/bots';
import { expect, test, type App } from '../src/fixtures';
import {
	answerFor,
	answersLeaving,
	askToStop,
	bastaBy,
	expectServerHas,
	fillAnswers,
	joinByInviteLink,
	openRound,
	type OpenRoundOptions
} from '../src/flows';
import { expectUnobscured } from '../src/layout';
import { expectNoSpanish } from '../src/no-spanish';
import type { Player } from '../src/player';
import { BASTA_BUTTON, BASTA_OVERLAY, CLASSIC_CATEGORIES, STOP_VOTE_SECONDS } from '../src/rules';

test.describe.configure({ mode: 'parallel' });

/** Ana (host), Ben and Cleo on the playing screen of round 1. */
async function table(app: App, options: OpenRoundOptions = {}) {
	const round = await openRound(app, options);
	const [ana, ben, cleo] = round.players as [Player, Player, Player];
	return { ...round, ana, ben, cleo };
}

/** Everybody records the Basta overlay (it lasts under a second); then it can be read with `expectSplash`. */
const watchSplashes = (players: Player[]) =>
	Promise.all(players.map((p) => p.playing.watchSplash()));

const reviewAndSummary = async (host: Player, players: Player[]) => {
	await Promise.all(players.map((p) => p.expectScreen('review')));
	await host.review.finish({ anyway: true });
	await Promise.all(players.map((p) => p.expectScreen('summary')));
};

test.describe('Spanish', () => {
	const categories = CLASSIC_CATEGORIES.es;

	test("everybody says yes: it counts as the asker's Basta, her empty cell scores 0", async ({
		app
	}) => {
		const { players, ana, ben, cleo, letter: L } = await table(app);
		const anaAnswers = answersLeaving(L, ana, [5]);
		const benAnswers = answersLeaving(L, ben, [4]);
		const cleoAnswers = answersLeaving(L, cleo, [4]);
		anaAnswers[0] = `${L}aaa`; // Ana and Ben share the first answer: 5 points each
		benAnswers[0] = `${L}aaa`;

		await test.step('answers missing: the button stays enabled and says what it will do', async () => {
			await Promise.all([
				fillAnswers(ana, anaAnswers),
				fillAnswers(ben, benAnswers),
				fillAnswers(cleo, cleoAnswers)
			]);
			await expectServerHas(ben, ana, 5, 6);
			await expectServerHas(ana, ben, 5, 6);
			await expectServerHas(ana, cleo, 5, 6);
			await expect(ana.playing.bastaButton).toBeEnabled();
			await expect(ana.playing.bastaButton).toHaveText(BASTA_BUTTON.es);
			await expect(ana.playing.bastaButton).toHaveAttribute('aria-haspopup', 'dialog');
			await expect(ana.playing.hint(ana.t('play_hint_ask_one'))).toBeVisible();
		});

		await test.step('the tap opens a confirmation (focus on "keep playing"); Escape closes it and sends nothing', async () => {
			await ana.playing.openAskDialog();
			const dialog = ana.playing.askDialog; // found by role and exact name
			await expect(ana.playing.askKeep).toBeFocused();
			await expect(dialog).toHaveAccessibleDescription(
				`${ana.t('play_ask_missing_one')} ${ana.t('play_ask_rule', { seconds: STOP_VOTE_SECONDS })}`
			);
			await ana.page.keyboard.press('Escape');
			await expect(dialog).toBeHidden();
			await expect(ana.playing.bastaButton).toBeFocused();
			expect(ana.sentCount('ask_stop'), 'Escape asked nobody').toBe(0);
			expect(ana.sentCount('basta')).toBe(0);
			for (const p of players) await p.playing.expectNoVote();
		});

		await test.step('confirmed: Ana waits ("0 de 2"), Ben and Cleo get a prompt that does not block them', async () => {
			await ana.playing.askEveryoneToStop();
			expect(ana.sentCount('ask_stop')).toBe(1);
			await ana.playing.expectTally(0, 2);
			expect(await ana.playing.voteSecondsLeft()).toBeLessThanOrEqual(STOP_VOTE_SECONDS);
			await expect(ana.playing.bastaButton).toBeDisabled();
			await expect(ana.playing.hint(ana.t('play_hint_ask_wait'))).toBeVisible();
			await expect(ana.playing.field(categories[5] as string)).toBeEnabled(); // she can keep typing
			await expect(ana.playing.votePrompt).toHaveCount(0);

			for (const voter of [ben, cleo]) {
				await expect(voter.playing.voteTitle('Ana')).toBeVisible();
				await expect(voter.playing.voteYes).toBeEnabled();
				await expect(voter.playing.voteNo).toBeEnabled();
				await expect(voter.playing.announcement).toHaveText(
					voter.t('play_vote_title', { name: 'Ana' })
				);
				await expect(voter.page.getByRole('dialog')).toHaveCount(0);
				await expect(voter.page.getByRole('alertdialog')).toHaveCount(0);
				await expect(voter.page.locator('[aria-modal="true"]')).toHaveCount(0);

				// the prompt is not modal: the missing answer is typed while it is open
				const color = voter.playing.field(categories[4] as string);
				const typed = answerFor(L, voter, 4);
				await color.fill(typed);
				await expect(color).toHaveValue(typed);
				await expect(color).toBeFocused();
				await expect(voter.playing.votePrompt).toBeVisible();
			}
		});

		await test.step('Ben says yes: "1 de 2" for Ana, "waiting" for Ben, Cleo is still asked', async () => {
			await ben.playing.voteYes.click();
			await ana.playing.expectTally(1, 2);
			await expect(ben.playing.voteWaiting(1, 2)).toBeVisible();
			await expect(ben.playing.voteYes).toHaveCount(0);
			await expect(cleo.playing.voteYes).toBeVisible();
			await expect(ana.playing.waitBanner).toBeVisible();
		});

		await test.step('Cleo says yes: Basta by Ana for everybody, nobody had to send one', async () => {
			await watchSplashes(players);
			await cleo.playing.voteYes.click();
			for (const p of players) await p.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
			for (const voter of [ben, cleo]) {
				await expect(voter.playing.graceBanner).toContainText(
					voter.t('play_caller_called', { name: 'Ana' })
				);
				await expect(voter.playing.announcement).toHaveText(`Ana: ${BASTA_OVERLAY.es}`);
			}
			await expect(ana.playing.graceBanner).toContainText(ana.t('play_you_called'));
			for (const p of players) await p.playing.expectNoVote();
			expect(ana.sentCount('basta'), 'the vote passed, Ana did not call Basta').toBe(0);
			expect(ben.sentCount('vote_stop')).toBe(1);
			expect(cleo.sentCount('vote_stop')).toBe(1);
		});

		await test.step("review: what was typed during the vote counts, Ana's empty cell has no points", async () => {
			await Promise.all(players.map((p) => p.expectScreen('review')));
			for (const p of players) {
				await p.review.expectCategory(categories[0] as string, 1, 6);
				await p.review.expectPoints('Ana', 5);
				await p.review.expectPoints('Ben', 5);
				await p.review.expectPoints('Cleo', 10);
			}
			await ana.review.jump(5).click();
			for (const p of players) {
				await p.review.expectCategory(categories[4] as string, 5, 6);
				await p.review.expectAnswer('Ben', answerFor(L, ben, 4)); // typed while the prompt was open
				await p.review.expectAnswer('Cleo', answerFor(L, cleo, 4));
				for (const name of ['Ana', 'Ben', 'Cleo']) await p.review.expectPoints(name, 10);
			}
			await ana.review.jump(6).click();
			for (const p of players) {
				await p.review.expectCategory(categories[5] as string, 6, 6);
				await p.review.expectNoPoints('Ana'); // nothing written: nothing to score
				await p.review.expectPoints('Ben', 10);
				await p.review.expectPoints('Cleo', 10);
			}
		});

		await test.step('totals: Ana 5+10+10+10+10+0, Ben 5+10×5, Cleo 10×6', async () => {
			await ana.review.finish({ anyway: true });
			for (const p of players) {
				await p.expectScreen('summary');
				await p.summary.expectTotals({ Ana: 45, Ben: 55, Cleo: 60 });
				await p.summary.expectRoundPoints({ Ana: 45, Ben: 55, Cleo: 60 });
				await p.summary.expectRank('Cleo', 1);
				await p.summary.expectRank('Ben', 2);
				await p.summary.expectRank('Ana', 3);
			}
		});
	});

	test('a no rejects it at once (only the asker is told, nobody who), and she can ask again right away', async ({
		app
	}) => {
		const { players, ana, ben, cleo, letter: L } = await table(app);
		await Promise.all([
			fillAnswers(ana, answersLeaving(L, ana, [5])),
			fillAnswers(ben, answersLeaving(L, ben)),
			fillAnswers(cleo, answersLeaving(L, cleo, [4]))
		]);
		await expectServerHas(ben, ana, 5, 6);
		await expectServerHas(ana, cleo, 5, 6);
		const declined = ana.t('play_stop_declined');

		await test.step('Ben says no: Ana gets the toast, every prompt and the banner go away', async () => {
			await askToStop(ana, [ben, cleo]);
			await ana.playing.expectTally(0, 2);
			await ben.playing.voteNo.click();
			await expect(ana.playing.toast(declined)).toBeVisible();
			for (const p of players) await p.playing.expectNoVote();
			await expect(ana.playing.toast(declined)).toHaveCount(1); // one toast, which names nobody
			for (const voter of [ben, cleo]) {
				await expect(voter.playing.toast(declined), 'only the asker hears it').toHaveCount(0);
				await expect(voter.playing.graceBanner).toHaveCount(0);
			}
			await expect(ana.playing.graceBanner).toHaveCount(0);
			for (const p of players) await p.expectScreen('playing');
			expect(ben.sentCount('vote_stop')).toBe(1);
			expect(cleo.sentCount('vote_stop'), 'Cleo never had to answer').toBe(0);
		});

		await test.step('no cooldown: the button works at once and everybody is asked again from zero', async () => {
			await expect(ana.playing.bastaButton).toBeEnabled();
			await expect(ana.playing.hint(ana.t('play_hint_ask_one'))).toBeVisible();
			await askToStop(ana, [ben, cleo]);
			await ana.playing.expectTally(0, 2); // Ben's earlier no is not remembered
			await expect(ben.playing.voteYes).toBeEnabled();
			await expect(ben.playing.voteNo).toBeEnabled();
			expect(ana.sentCount('ask_stop')).toBe(2);
		});

		await test.step('this time both say yes: Basta by Ana, review, scores', async () => {
			await watchSplashes(players);
			await ben.playing.voteYes.click();
			await cleo.playing.voteYes.click();
			for (const p of players) await p.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
			await reviewAndSummary(ana, players);
			for (const p of players) await p.summary.expectTotals({ Ana: 50, Ben: 60, Cleo: 50 });
			expect(ben.sentCount('vote_stop')).toBe(2);
			expect(cleo.sentCount('vote_stop')).toBe(1);
		});
	});

	test('with the setting off the button waits for every field, as it always did', async ({
		app
	}) => {
		const {
			players,
			ana,
			ben,
			cleo,
			letter: L
		} = await table(app, {
			lobby: async (host, guests) => {
				await expect(host.lobby.stopVote, 'on by default').toHaveAttribute('aria-checked', 'true');
				await expect(host.lobby.stopVote).toHaveAccessibleDescription(host.t('lobby_stop_on'));
				for (const g of guests) {
					await expect(g.lobby.stopChip).toHaveText(g.t('lobby_summary_stop_on'));
				}
				await host.lobby.setStopVote(false);
				await expect(host.lobby.stopVote).toHaveAccessibleDescription(host.t('lobby_stop_off'));
				for (const g of guests) {
					await expect(g.lobby.stopChip).toHaveText(g.t('lobby_summary_stop_off'));
					await expect(g.lobby.stopVote, 'guests have no switch').toHaveCount(0);
				}
			}
		});

		await fillAnswers(ana, answersLeaving(L, ana, [5]));
		await expect(ana.playing.bastaButton).toBeDisabled();
		await expect(ana.playing.hint(ana.t('play_hint_fill'))).toBeVisible();
		const box = await ana.playing.bastaButton.boundingBox();
		if (!box) throw new Error('the button has no box');
		await ana.page.touchscreen.tap(box.x + box.width / 2, box.y + box.height / 2); // a finger on a disabled button
		await expect(ana.playing.askDialog).toHaveCount(0);
		expect(ana.sentCount('ask_stop')).toBe(0);
		expect(ana.sentCount('basta')).toBe(0);

		await ana.playing.fill(categories[5] as string, answerFor(L, ana, 5));
		await expect(ana.playing.bastaButton).toBeEnabled();
		await expect(ana.playing.hint(ana.t('play_hint_ready'))).toBeVisible();
		await bastaBy(ana, [ben, cleo]);
		await reviewAndSummary(ana, players);
		for (const p of players) await p.summary.expectTotals({ Ana: 60, Ben: 0, Cleo: 0 });
		expect(ana.sentCount('basta')).toBe(1);
		expect(ana.sentCount('ask_stop')).toBe(0);
	});

	test('somebody with everything valid calls Basta while the vote is open: the vote ends, the overlay names that player', async ({
		app
	}) => {
		const { players, ana, ben, cleo, letter: L } = await table(app);
		await Promise.all([
			fillAnswers(ana, answersLeaving(L, ana, [5])),
			fillAnswers(ben, answersLeaving(L, ben)),
			fillAnswers(cleo, answersLeaving(L, cleo, [4]))
		]);
		await expectServerHas(ben, ana, 5, 6);
		await expectServerHas(ana, ben, 6, 6);
		await askToStop(ana, [ben, cleo]);
		await expect(ben.playing.bastaButton).toBeEnabled();
		await expect(ben.playing.hint(ben.t('play_hint_ready'))).toBeVisible();

		await watchSplashes(players);
		await ben.playing.bastaButton.click();
		for (const p of players) await p.playing.expectSplash(['Ben', BASTA_OVERLAY.es]);
		for (const p of players) await p.playing.expectNoVote();
		for (const p of [ana, cleo]) {
			await expect(p.playing.graceBanner).toContainText(p.t('play_caller_called', { name: 'Ben' }));
		}
		for (const text of ['play_stop_declined', 'play_stop_timeout']) {
			await expect(ana.playing.toast(ana.t(text)), 'an ended vote is not a refusal').toHaveCount(0);
		}
		expect(ben.sentCount('basta')).toBe(1);
		expect(ana.sentCount('basta')).toBe(0);
		expect(cleo.sentCount('vote_stop')).toBe(0);

		await reviewAndSummary(ana, players);
		for (const p of players) await p.summary.expectTotals({ Ana: 50, Ben: 60, Cleo: 50 });
	});

	test('a voter who goes offline leaves the electorate: the remaining yes votes pass it', async ({
		app
	}) => {
		const {
			players,
			ana,
			ben,
			cleo,
			letter: L
		} = await table(app, {
			playerOptions: (name) => ({ network: name === 'Ben' })
		});
		await fillAnswers(ana, answersLeaving(L, ana, [5]));
		await askToStop(ana, [ben, cleo]);
		await ana.playing.expectTally(0, 2);

		await ben.network?.drop();
		await ana.playing.expectTally(0, 1); // the electorate is live: only Cleo can still answer
		await expect(cleo.playing.voteTitle('Ana')).toBeVisible();

		await watchSplashes([ana, cleo]);
		await cleo.playing.voteYes.click();
		for (const p of [ana, cleo]) await p.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
		await ben.network?.restore();
		await ben.expectScreen('review'); // he comes back to a round that moved on without him
		await Promise.all(players.map((p) => p.expectScreen('review')));
	});

	test('a voter who closes the tab after the others said yes lets it pass', async ({ app }) => {
		const { ana, ben, cleo, letter: L } = await table(app);
		await fillAnswers(ana, answersLeaving(L, ana, [5]));
		await askToStop(ana, [ben, cleo]);
		await cleo.playing.voteYes.click();
		await ana.playing.expectTally(1, 2);
		await expect(ben.playing.voteTitle('Ana')).toBeVisible();

		await watchSplashes([ana, cleo]);
		await ben.page.close();
		for (const p of [ana, cleo]) await p.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
		for (const p of [ana, cleo]) await p.expectScreen('review');
	});

	test('the asker going offline cancels the vote: nobody is asked any more, nothing passes, a new one starts from zero', async ({
		app
	}) => {
		const {
			players,
			ana,
			ben,
			cleo,
			letter: L
		} = await table(app, {
			playerOptions: (name) => ({ network: name === 'Ana' })
		});
		await fillAnswers(ana, answersLeaving(L, ana, [5]));
		await askToStop(ana, [ben, cleo]);
		await ben.playing.voteYes.click();
		await expect(ben.playing.voteWaiting(1, 2)).toBeVisible();

		await ana.network?.drop();
		for (const voter of [ben, cleo]) {
			await voter.playing.expectNoVote();
			await expect(voter.playing.graceBanner).toHaveCount(0);
			await expect(voter.playing.toast(voter.t('play_stop_declined'))).toHaveCount(0);
			await voter.expectScreen('playing');
		}

		await ana.network?.restore();
		await ana.playing.expectNoVote();
		await expect(ana.playing.bastaButton).toBeEnabled();
		await askToStop(ana, [ben, cleo]);
		await ana.playing.expectTally(0, 2); // Ben's yes died with the old vote
		await watchSplashes(players);
		await ben.playing.voteYes.click();
		await cleo.playing.voteYes.click();
		for (const p of players) await p.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
	});

	test('a spectator only sees that somebody asked, and cannot vote (nor does she count)', async ({
		app
	}) => {
		const { players, code, ana, ben, cleo, letter: L } = await table(app);
		const dan = await app.player('Dan');
		await joinByInviteLink(dan, code, { landing: 'playing' });
		const eli = await Bot.join(code, 'Eli'); // a second spectator, to speak to the server directly
		app.onDispose(() => eli.close());
		await expect(dan.playing.spectatorNote).toBeVisible();

		await fillAnswers(ana, answersLeaving(L, ana, [5]));
		await askToStop(ana, [ben, cleo]);
		await expect(dan.playing.onlooker('Ana')).toBeVisible();
		await expect(dan.playing.votePrompt).toHaveCount(0);
		await expect(dan.playing.voteYes).toHaveCount(0);
		await expect(dan.playing.voteNo).toHaveCount(0);
		await expect(dan.playing.bastaButton).toHaveCount(0);
		await expect(dan.playing.fields).toHaveCount(0);

		eli.send('vote_stop', { yes: true });
		await expect
			.poll(() => eli.errors, { message: 'the server refuses a spectator' })
			.toContain('not_participant');
		await ana.playing.expectTally(0, 2); // two spectators in the room: still two voters

		await watchSplashes([ana, dan]);
		await ben.playing.voteYes.click();
		await cleo.playing.voteYes.click();
		await dan.playing.expectSplash(['Ana', BASTA_OVERLAY.es]);
		await expect(dan.playing.graceBanner).toContainText(
			dan.t('play_caller_called', { name: 'Ana' })
		);
		await dan.playing.expectNoVote();
		expect(dan.sentCount('vote_stop')).toBe(0);
		await Promise.all([...players, dan].map((p) => p.expectScreen('review')));
	});
});

const LANGUAGE: Record<'en' | 'nl', string> = { en: 'English', nl: 'Dutch' };

for (const locale of ['en', 'nl'] as const) {
	test(`the whole vote in ${LANGUAGE[locale]}: exact words, "Stop!" and nothing Spanish on any screen`, async ({
		app
	}) => {
		const {
			players,
			ana,
			ben,
			cleo,
			letter: L
		} = await table(app, {
			locale,
			lobby: async (host, guests) => {
				await expect(host.lobby.stopVote).toHaveAttribute('aria-checked', 'true');
				await expectNoSpanish([host, ...guests], locale, 'lobby (vote to stop on)');
				await host.lobby.setStopVote(false);
				for (const g of guests) {
					await expect(g.lobby.stopChip).toHaveText(g.t('lobby_summary_stop_off'));
				}
				await expectNoSpanish([host, ...guests], locale, 'lobby (vote to stop off)');
				await host.lobby.setStopVote(true);
				for (const g of guests) {
					await expect(g.lobby.stopChip).toHaveText(g.t('lobby_summary_stop_on'));
				}
			}
		});
		const categories = CLASSIC_CATEGORIES[locale];
		await Promise.all([
			fillAnswers(ana, answersLeaving(L, ana, [5])),
			fillAnswers(ben, answersLeaving(L, ben, [4])),
			fillAnswers(cleo, answersLeaving(L, cleo))
		]);
		await expectServerHas(ben, ana, 5, 6);
		await expectServerHas(ana, ben, 5, 6);

		await test.step('the button says "Stop!" and offers the vote', async () => {
			await expect(ana.playing.bastaButton).toHaveText(BASTA_BUTTON[locale]);
			await expect(ana.playing.bastaButton).toBeEnabled();
			await expect(ana.playing.hint(ana.t('play_hint_ask_one'))).toBeVisible();
			await ana.playing.openAskDialog();
			await expect(ana.playing.askKeep).toBeFocused();
			await expectNoSpanish([ana], locale, 'ask confirmation');
			await ana.playing.askKeep.click();
			await expect(ana.playing.askDialog).toBeHidden();
			await expect(ana.playing.bastaButton).toBeFocused();
		});

		await test.step('asked: banner and prompt, then Ben types and says no', async () => {
			await askToStop(ana, [ben, cleo]);
			await ana.playing.expectTally(0, 2);
			await expectNoSpanish(players, locale, 'vote open');
			const color = ben.playing.field(categories[4] as string);
			await color.fill(answerFor(L, ben, 4));
			await expect(color).toBeFocused();
			await expect(ben.playing.votePrompt).toBeVisible();
			await ben.playing.voteNo.click();
			await expect(ana.playing.toast(ana.t('play_stop_declined'))).toBeVisible();
			for (const p of players) await p.playing.expectNoVote();
			await expectNoSpanish(players, locale, 'vote declined');
		});

		await test.step('asked again at once: everybody says yes, "Stop!" for all with Ana\'s name', async () => {
			await askToStop(ana, [ben, cleo]);
			await ana.playing.expectTally(0, 2);
			await watchSplashes(players);
			await ben.playing.voteYes.click();
			await expect(ben.playing.voteWaiting(1, 2)).toBeVisible();
			await ana.playing.expectTally(1, 2);
			await cleo.playing.voteYes.click();
			for (const p of players) await p.playing.expectSplash(['Ana', BASTA_OVERLAY[locale]]);
			for (const voter of [ben, cleo]) {
				await expect(voter.playing.graceBanner).toContainText(
					voter.t('play_caller_called', { name: 'Ana' })
				);
				await expect(voter.playing.announcement).toHaveText(`Ana: ${BASTA_OVERLAY[locale]}`);
			}
			await expectNoSpanish(players, locale, 'grace');
		});

		await test.step('review and totals: Ana 50 (one empty cell), Ben 60, Cleo 60', async () => {
			await Promise.all(players.map((p) => p.expectScreen('review')));
			await expectNoSpanish(players, locale, 'review');
			await ana.review.finish({ anyway: true });
			for (const p of players) {
				await p.expectScreen('summary');
				await p.summary.expectTotals({ Ana: 50, Ben: 60, Cleo: 60 });
			}
			await expectNoSpanish(players, locale, 'summary');
		});
	});
}

test('on a 360×740 phone the prompt never hides the field being typed in (Dutch, a long name)', async ({
	app
}) => {
	const longName = 'Wilhelminajohann'; // 16 characters, the longest nickname
	const phone = { width: 360, height: 740 };
	const {
		players,
		ana,
		ben,
		cleo,
		letter: L
	} = await table(app, {
		locale: 'nl',
		names: [longName, 'Ben', 'Cleo'],
		playerOptions: () => ({ viewport: phone })
	});
	const categories = CLASSIC_CATEGORIES.nl;
	const fieldBox = (p: Player, i: number) => p.playing.field(categories[i] as string);

	await Promise.all([
		fillAnswers(ana, answersLeaving(L, ana, [5])),
		fillAnswers(ben, answersLeaving(L, ben, [4, 5])),
		fillAnswers(cleo, answersLeaving(L, cleo))
	]);
	await expectServerHas(cleo, ben, 4, 6);

	await test.step('the confirmation fits the screen and its buttons are easy to hit', async () => {
		await ana.playing.openAskDialog();
		for (const button of [ana.playing.askConfirm, ana.playing.askKeep]) {
			await expectUnobscured(button, 'a button of the confirmation');
			expect((await button.boundingBox())?.height).toBeGreaterThanOrEqual(48);
		}
		await ana.playing.askKeep.click();
		await expect(ana.playing.askDialog).toBeHidden();
	});

	await test.step('Ben is typing in the last field when the prompt opens: it stays in view', async () => {
		await fieldBox(ben, 5).focus(); // what the keyboard's Enter does: the browser scrolls it into view
		await ben.page.keyboard.type(answerFor(L, ben, 5).slice(0, 3));
		await expect(fieldBox(ben, 5)).toBeFocused();
		await expectUnobscured(fieldBox(ben, 5), 'the last field before the vote');
		await askToStop(ana, [ben, cleo]);
		await expect(ben.playing.voteTitle(longName)).toBeVisible();
		await expectUnobscured(fieldBox(ben, 5), 'the focused last field with the prompt open');
		await expectUnobscured(ben.playing.votePrompt, 'the prompt');
		await expectUnobscured(ben.playing.voteYes, 'the yes button');
		await expectUnobscured(ben.playing.voteNo, 'the no button');
		await expectUnobscured(ben.playing.bastaButton, 'the main button');
		for (const button of [ben.playing.voteYes, ben.playing.voteNo, ben.playing.bastaButton]) {
			expect((await button.boundingBox())?.height).toBeGreaterThanOrEqual(48);
		}
	});

	await test.step('every field Ben moves to is fully visible while the prompt is open', async () => {
		for (const i of [0, 1, 2, 3, 4, 5, 3, 0]) {
			await fieldBox(ben, i).focus();
			await expect(fieldBox(ben, i)).toBeFocused();
			await expectUnobscured(fieldBox(ben, i), `the ${categories[i]} field`);
		}
		await expect(ben.playing.votePrompt).toBeVisible(); // still asking all along
		await ben.playing.voteNo.click();
		for (const p of players) await p.playing.expectNoVote();
	});
});
