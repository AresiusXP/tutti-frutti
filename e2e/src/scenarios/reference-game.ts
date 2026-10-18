import { expect, test, type App } from '../fixtures';
import {
	answerFor,
	bastaBy,
	chooserStops,
	createRoom,
	expectServerHas,
	fillAnswers,
	joinByCode,
	joinByInviteLink,
	quickRound,
	startGame,
	uniqueAnswers
} from '../flows';
import type { Locale } from '../messages';
import { ROOM_CODE } from '../pages/home';
import type { Player } from '../player';
import { BASTA_OVERLAY, CLASSIC_CATEGORIES, playableLetters } from '../rules';

export type PhaseName = 'lobby' | 'letter' | 'playing' | 'grace' | 'review' | 'summary' | 'final';

export interface ReferenceGameOptions {
	/** Called on every player's screen at each phase (the English test checks for Spanish text). */
	onScreen?: (players: Player[], phase: PhaseName) => Promise<void>;
}

/**
 * The reference game: three players, every phase, every number checked against a hand-computed
 * table. The UI language is `locale`; a room created from a Spanish UI is a Spanish lobby (the
 * six "Clásicas" categories), from an English UI an English one, from a Dutch UI a Dutch one
 * ("Klassiekers").
 *
 *   category      Ana    Beto   Caro      points (Ana, Beto, Caro)
 *   0 first       Laaa   Laaa   Lbbb      5  5  10   (then Beto and Caro reject Ana's: 0 10 10)
 *   1 second      own    own    own       10 10 10
 *   2 third       Lccc   Lccc   Lccc      5  5  5   (Dutch: Lĳst, Lijst, LIJST: the ligature is "ij")
 *   3 fourth      Lddd   Leee   Lddd      5  10 5
 *   4 fifth       Lfff   -      Lggg      10 0  10
 *   5 sixth       own    own    own       10 10 10
 *   round 1 totals                        Ana 40, Beto 45, Caro 50
 *   round 2: Caro fills all (60), Ana three fields (30), Beto none -> Ana 70, Beto 45, Caro 110
 */
export async function playReferenceGame(
	app: App,
	locale: Locale,
	options: ReferenceGameOptions = {}
): Promise<void> {
	const onScreen = options.onScreen ?? (async () => undefined);
	const ana = await app.player('Ana', { locale });
	const beto = await app.player('Beto', { locale });
	const caro = await app.player('Caro', { locale });
	const everyone = [ana, beto, caro];
	const categories = CLASSIC_CATEGORIES[locale];
	const overlay = BASTA_OVERLAY[locale];

	await test.step('Ana creates a room and is alone in it', async () => {
		const code = await createRoom(app, ana, { emoji: '🍓', color: 'strawberry' });
		expect(code).toMatch(ROOM_CODE);
		await ana.lobby.expectPlayers(['Ana']);
		await ana.lobby.expectHost('Ana');
		await expect(ana.lobby.startButton).toBeDisabled();
		await expect(ana.page.getByText(ana.t('lobby_start_hint', { min: 2 }))).toBeVisible();
		await onScreen([ana], 'lobby');
	});

	const code = ana.code;

	await test.step('Beto joins with the code, Caro with the invite link (lowercase)', async () => {
		await joinByCode(beto, code, { emoji: '🍇', color: 'grape' });
		await joinByInviteLink(caro, code, { lowercase: true });
		for (const p of everyone) {
			await p.lobby.expectPlayers(['Ana', 'Beto', 'Caro']);
			await p.lobby.expectHost('Ana');
		}
		// avatars travel through the server
		await expect(caro.lobby.playerRow('Ana')).toContainText('🍓');
		await expect(caro.lobby.playerRow('Beto')).toContainText('🍇');
		await expect(beto.lobby.waitingForHost('Ana')).toBeVisible();
		await expect(ana.lobby.startButton).toBeEnabled();
	});

	await test.step('the host changes the rounds; guests see the new settings', async () => {
		await ana.lobby.setRounds(2);
		for (const guest of [beto, caro]) {
			await expect(guest.lobby.chips).toHaveText([
				guest.t(`lobby_lang_${locale}`),
				guest.t('lobby_summary_hard_on', { count: playableLetters(locale, true).length }),
				guest.t('lobby_summary_rounds', { n: 2 }),
				guest.t('lobby_summary_time_off'),
				guest.t('lobby_summary_stop_on')
			]);
			await guest.lobby.expectCategories(categories);
		}
		await onScreen(everyone, 'lobby');
	});

	await startGame(ana, everyone);

	let letter1 = '';

	await test.step('round 1: the host (first chooser) stops the letter', async () => {
		await expect(beto.letter.choosing('Ana')).toBeVisible();
		await expect(caro.letter.choosing('Ana')).toBeVisible();
		await onScreen(everyone, 'letter');
		const chooser = await chooserStops(everyone);
		expect(chooser).toBe(ana);
		const letters = await Promise.all(everyone.map((p) => p.playing.letter()));
		expect(new Set(letters).size, 'everybody sees the same letter').toBe(1);
		letter1 = letters[0] as string;
		expect(letter1).toMatch(/^[A-ZÑ]$/);
		for (const p of everyone) {
			await expect(p.playing.fields).toHaveCount(6);
			await expect(p.playing.timer, 'no time limit, no clock').toHaveCount(0);
			for (const label of categories) await expect(p.playing.field(label)).toBeVisible();
		}
	});

	const L = letter1;
	// Dutch: the third category is written three ways that are the same word (the ligature ĳ, "ij", capitals)
	const third =
		locale === 'nl'
			? { Ana: `${L}ĳst`, Beto: `${L}ijst`, Caro: `${L}IJST` }
			: { Ana: `${L}ccc`, Beto: `${L}ccc`, Caro: `${L}ccc` };
	const answers: Record<string, (string | null)[]> = {
		Ana: [
			`${L}aaa`,
			answerFor(L, 'Ana', 1),
			third.Ana,
			`${L}ddd`,
			`${L}fff`,
			answerFor(L, 'Ana', 5)
		],
		Beto: [
			`${L}aaa`,
			answerFor(L, 'Beto', 1),
			third.Beto,
			`${L}eee`,
			null,
			answerFor(L, 'Beto', 5)
		],
		Caro: [
			`${L}bbb`,
			answerFor(L, 'Caro', 1),
			third.Caro,
			`${L}ddd`,
			`${L}ggg`,
			answerFor(L, 'Caro', 5)
		]
	};

	await test.step("everybody writes; with a field missing the button only offers to ask everyone to stop; nobody sees the others' answers", async () => {
		await Promise.all(everyone.map((p) => fillAnswers(p, answers[p.name] ?? [])));
		// Beto left one field empty: his button does not call Basta, it opens the confirmation (and he keeps playing)
		await expect(beto.playing.bastaButton).toBeEnabled();
		await expect(beto.playing.hint(beto.t('play_hint_ask_one'))).toBeVisible();
		await beto.playing.openAskDialog();
		await beto.playing.askKeep.click();
		await expect(beto.playing.askDialog).toBeHidden();
		expect(beto.sentCount('basta'), 'no Basta with a field missing').toBe(0);
		expect(beto.sentCount('ask_stop'), 'nothing was asked').toBe(0);
		for (const p of everyone) await p.playing.expectNoVote();
		await expectServerHas(ana, beto, 5, 6);
		await expectServerHas(ana, caro, 6, 6);
		await expectServerHas(caro, ana, 6, 6);
		// only the fill progress is visible; the answers themselves stay hidden until the review
		await expect(ana.page.locator('body')).not.toContainText(`${L}bbb`);
		await expect(ana.page.locator('body')).not.toContainText(answerFor(L, 'Caro', 1));
		await onScreen(everyone, 'playing');
	});

	await test.step('Ana calls Basta: the others get the overlay and the countdown', async () => {
		await bastaBy(ana, [beto, caro]);
		for (const other of [beto, caro]) {
			await other.playing.expectSplash(['Ana', overlay]);
			await expect(other.playing.graceBanner).toContainText(
				other.t('play_caller_called', { name: 'Ana' })
			);
			await expect(other.playing.announcement).toHaveText(`Ana: ${overlay}`);
		}
		await expect(ana.playing.graceBanner).toContainText(ana.t('play_you_called'));
		await onScreen(everyone, 'grace');
	});

	await test.step('review: the host walks every category and everybody follows', async () => {
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
		for (const p of everyone) await p.review.expectCategory(categories[0] as string, 1, 6);
		for (const [i, label] of categories.entries()) {
			if (i > 0) await ana.review.next.click();
			for (const p of everyone) await p.review.expectCategory(label, i + 1, 6);
			if (locale === 'nl' && i === 2) {
				for (const p of everyone) {
					for (const name of ['Ana', 'Beto', 'Caro'] as const) {
						await p.review.expectAnswer(name, third[name]); // shown as typed ...
						await p.review.expectPoints(name, 5); // ... and counted as one answer, shared by three
					}
				}
			}
		}
		await expect(ana.review.next).toBeDisabled();
		await expect(beto.review.next).toHaveCount(0);
		await expect(beto.review.hostControls('Ana')).toBeVisible();
		await ana.review.jump(1).click();
		for (const p of everyone) await p.review.expectCategory(categories[0] as string, 1, 6);
	});

	await test.step('review: shared answers show 5, a unique one 10; two rejections knock Ana out', async () => {
		for (const p of everyone) {
			await p.review.expectAnswer('Ana', `${L}aaa`);
			await p.review.expectAnswer('Caro', `${L}bbb`);
			await p.review.expectPoints('Ana', 5);
			await p.review.expectPoints('Beto', 5);
			await p.review.expectPoints('Caro', 10);
		}
		await expect(ana.review.rejectButton('Beto')).toBeVisible();
		await expect(ana.review.rejectButton('Ana')).toHaveCount(0); // nobody votes on their own answer

		// 3 players: more than half of the 2 others must vote, so it takes both
		await beto.review.reject('Ana');
		await expect(caro.review.rejectButton('Ana', { votes: 1, needed: 2 })).toBeVisible();
		await expect(caro.review.badge('Ana')).toHaveAttribute('data-points', '5');

		await caro.review.reject('Ana');
		for (const p of everyone) {
			await p.review.expectPoints('Ana', 0);
			await p.review.expectPoints('Beto', 10); // no longer shares his answer with the rejected one
			await p.review.expectPoints('Caro', 10);
		}
		await expect(
			ana.review.card('Ana').getByText(ana.t('review_rejected'), { exact: true })
		).toBeVisible();
		await onScreen(everyone, 'review');
	});

	await test.step('summary: totals equal the hand-computed table', async () => {
		await ana.review.finish();
		for (const p of everyone) {
			await p.expectScreen('summary');
			await p.summary.expectTotals({ Ana: 40, Beto: 45, Caro: 50 });
			await p.summary.expectRoundPoints({ Ana: 40, Beto: 45, Caro: 50 });
			await p.summary.expectRank('Caro', 1);
			await p.summary.expectRank('Beto', 2);
			await p.summary.expectRank('Ana', 3);
		}
		await expect(ana.summary.nextRoundButton).toBeVisible();
		await expect(beto.summary.waiting).toBeVisible();
		await expect(ana.summary.lastRoundNote).toHaveCount(0);
		await onScreen(everyone, 'summary');
	});

	await test.step('round 2: the next player in join order chooses, Caro calls Basta', async () => {
		await ana.summary.nextRound();
		await Promise.all(everyone.map((p) => p.expectScreen('letter')));
		const chooser = await chooserStops(everyone);
		expect(chooser, 'the second joiner chooses in round 2').toBe(beto);
		const { letter, round } = await caro.playing.roundInfo();
		expect(round).toBe(2);
		expect(letter, 'a letter is never played twice').not.toBe(letter1);

		await fillAnswers(caro, uniqueAnswers(letter, caro, 6));
		await fillAnswers(ana, [...uniqueAnswers(letter, ana, 3), null, null, null]);
		await expectServerHas(caro, ana, 3, 6);
		await bastaBy(caro, [ana, beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
	});

	await test.step('review: finishing early asks first, "keep reviewing" cancels', async () => {
		await ana.review.finishButton.click();
		await expect(ana.review.unseenWarning(1, 6)).toBeVisible();
		await ana.review.keepReviewing.click();
		await expect(ana.review.finishButton).toBeVisible();
		await ana.review.finish({ anyway: true });
	});

	await test.step('summary of the last round: scores add up and only "end game" is offered', async () => {
		for (const p of everyone) {
			await p.expectScreen('summary');
			await p.summary.expectTotals({ Ana: 70, Beto: 45, Caro: 110 });
			await p.summary.expectRoundPoints({ Ana: 30, Beto: 0, Caro: 60 });
			await expect(p.summary.lastRoundNote).toContainText(p.t('summary_last_round'));
		}
		await expect(ana.summary.nextRoundButton).toHaveCount(0);
		await ana.summary.endGame();
	});

	await test.step('final: podium and final scores', async () => {
		for (const p of everyone) {
			await p.expectScreen('final');
			await p.final.expectWinner('Caro');
			await expect(p.final.place(1)).toContainText('Caro');
			await expect(p.final.place(2)).toContainText('Ana');
			await expect(p.final.place(3)).toContainText('Beto');
			await p.final.expectTotals({ Caro: 110, Ana: 70, Beto: 45 });
		}
		await expect(ana.final.rematchButton).toBeVisible();
		await expect(beto.final.waitingForRematch).toBeVisible();
		await expect(beto.final.rematchButton).toHaveCount(0);
		await onScreen(everyone, 'final');
	});

	await test.step('rematch: back to the lobby with the same players and settings', async () => {
		await ana.final.rematchButton.click();
		for (const p of everyone) {
			await p.expectScreen('lobby');
			await p.lobby.expectPlayers(['Ana', 'Beto', 'Caro']);
		}
		await expect(beto.lobby.chips.nth(2)).toHaveText(beto.t('lobby_summary_rounds', { n: 2 }));
		await onScreen(everyone, 'lobby');
	});

	await test.step('leaving the lobby sends the player Home and frees the seat', async () => {
		await caro.lobby.leave();
		await expect(caro.page).toHaveURL(/\/$/);
		await expect(caro.home.createButton).toBeVisible();
		await ana.lobby.expectPlayers(['Ana', 'Beto']);
	});

	await test.step('the second game starts from zero (scores were reset)', async () => {
		await startGame(ana, [ana, beto]);
		const { round, chooser } = await quickRound({
			host: ana,
			everyone: [ana, beto],
			caller: beto,
			fields: 6
		});
		expect(round, 'the round counter restarted').toBe(1);
		expect(chooser).toBe(ana);
		await ana.summary.expectTotals({ Ana: 0, Beto: 60 });
		await beto.summary.expectTotals({ Ana: 0, Beto: 60 });
	});

	await test.step('ending the game and pressing Home leaves the room', async () => {
		await ana.summary.endGame();
		await beto.expectScreen('final');
		await beto.final.homeButton.click();
		await expect(beto.page).toHaveURL(/\/$/);
	});
}
