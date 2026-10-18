import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import {
	answersLeaving,
	bastaBy,
	chooserStops,
	createRoom,
	expectServerHas,
	joinByCode,
	joinByInviteLink,
	openRound,
	startGame,
	uniqueAnswers
} from '../src/flows';
import type { Locale } from '../src/messages';
import type { Player } from '../src/player';

test.describe.configure({ mode: 'parallel' });

const LOCALES: Locale[] = ['es', 'en', 'nl'];

/**
 * Axe on every screen of the game in each screen language, with reduced motion so entrance animations
 * cannot skew the contrast checks. A serious or critical finding fails (nothing is tolerated: the
 * allow-list in `src/a11y.ts` is empty), and so do the two moderate rules `expectAccessible` enforces:
 * one `h1` per screen and a name that contains the text a control shows (WCAG 2.5.3).
 *
 * Not reachable here: the Basta overlay (it lasts 0.9 s, shorter than an axe run) and the screens that
 * need the clock of the server (`04-time-limit.slow`). Both are covered on the dev playground by the
 * component sweep in `web/src/lib/a11y.test.ts` (every scenario, every language).
 */
for (const locale of LOCALES) {
	test(`no accessibility violations on the main screens (${locale})`, async ({ app }) => {
		const ana = await app.player('Ana', { locale, reducedMotion: true });
		const beto = await app.player('Beto', { locale, reducedMotion: true });
		const caro = await app.player('Caro', { locale, reducedMotion: true });
		const players = [ana, beto];

		await ana.home.open();
		await ana.home.setNickname('Ana');
		await expectAccessible(ana, 'home');

		const code = await createRoom(app, ana);
		await beto.page.goto(`/${code}`);
		await beto.gate.expectOpen(code);
		await expectAccessible(beto, 'gate');
		await beto.gate.setNickname('Beto');
		await beto.gate.join();
		await beto.lobby.expectVisible(code);
		await ana.lobby.expectPlayers(['Ana', 'Beto']);

		await test.step('the lobby, with a time limit chosen, and its sheets', async () => {
			await ana.lobby.setTimeLimit(180);
			await expectAccessible(ana, 'lobby (host, time limit chosen)');
			await expectAccessible(beto, 'lobby (guest)');

			await ana.lobby.qrButton.click();
			await ana.lobby.expectQrDrawn(code);
			await expectAccessible(ana, 'QR sheet');
			await ana.lobby.closeQr();

			await ana.lobby.askKick('Beto');
			await expectAccessible(ana, 'lobby (confirming to remove a player)');
			await ana.lobby.cancelKick('Beto');

			await ana.lobby.openLeave();
			await expectAccessible(ana, 'leave sheet');
			await ana.lobby.stay();

			await ana.picker.open();
			await ana.picker.expectLoaded();
			await expectAccessible(ana, 'category picker');
			await ana.picker.openCustom();
			await expect(ana.picker.customInput).toBeVisible();
			await expectAccessible(ana, 'category picker (custom category field)');
			await ana.picker.done();
		});

		await startGame(ana, players);

		await test.step('Caro arrives while the letter is being chosen and waits for the next round', async () => {
			await joinByInviteLink(caro, code, { landing: 'letter' });
			await expect(caro.letter.joinsNextRound).toBeVisible();
			await expectAccessible(caro, 'letter (joins next round)');
		});

		await expectAccessible(ana, 'letter (chooser)');
		await expectAccessible(beto, 'letter (waiting)');
		await chooserStops([ana, beto, caro]);

		const { letter } = await ana.playing.roundInfo();
		await ana.playing.fillAll(uniqueAnswers(letter, ana, 6));
		// Beto: four good answers, one empty field and one that does not start with the letter
		const wrong = letter === 'A' ? 'Bxx' : 'Axx';
		await beto.playing.fillAll([...uniqueAnswers(letter, beto, 4), null, wrong]);
		await expect(ana.playing.progress('Beto', 5, 6)).toBeAttached();
		await expectAccessible(ana, 'playing (time limit running)');
		await expectAccessible(beto, 'playing (empty and wrong-letter fields)');
		await expectAccessible(caro, 'playing (watching)');

		await bastaBy(ana, [beto, caro]);
		await Promise.all([ana, beto, caro].map((p) => p.expectScreen('review')));
		await expectAccessible(ana, 'review (host)');
		await expectAccessible(beto, 'review (guest)');
		await expectAccessible(caro, 'review (spectator)');

		await test.step('proposing that two answers are the same', async () => {
			await beto.review.sameToggle.click();
			await expect(beto.review.pickButton('Ana')).toBeVisible();
			await expectAccessible(beto, 'review (choosing the first answer)');
			await beto.review.pickButton('Ana').click();
			await expect(beto.review.sameCancel).toBeVisible();
			await expectAccessible(beto, 'review (choosing the second answer)');
			await beto.review.pickButton('Beto').click();
			await expect(ana.review.voteSame(false, 'Ana', 'Beto')).toBeVisible();
			await expectAccessible(ana, 'review (a proposal to vote on)');
			await expectAccessible(beto, 'review (the proposal made)');
			await ana.review.voteSame(false, 'Ana', 'Beto').click();
		});

		await test.step('an answer that was rejected is struck through and stays readable', async () => {
			await beto.review.reject('Ana');
			await expect(ana.review.rejectedChip).toBeVisible();
			await expectAccessible(ana, 'review (own answer rejected, host)');
			await expectAccessible(beto, 'review (voted to reject)');
		});

		await ana.review.finish({ anyway: true });

		await ana.expectScreen('summary');
		await expectAccessible(ana, 'summary (host)');
		await beto.expectScreen('summary');
		await expectAccessible(beto, 'summary (guest)');
		await caro.expectScreen('summary');
		await expectAccessible(caro, 'summary (joins next round)');
		await ana.summary.endGame();
		await ana.expectScreen('final');
		await expectAccessible(ana, 'final (host)');
		await beto.expectScreen('final');
		await expectAccessible(beto, 'final (guest)');
	});
}

/**
 * The vote to stop adds two surfaces the main-screens smoke never sees: the confirmation (a modal
 * `alertdialog`) and, for everybody else, a NON-modal prompt in the footer while a vote is open (plus the
 * asker's banner). Axe on each, in each screen language.
 */
for (const locale of LOCALES) {
	test(`no accessibility violations on the vote to stop (${locale})`, async ({ app }) => {
		const {
			players: [ana, ben, cleo],
			letter
		} = await openRound(app, { locale, playerOptions: () => ({ reducedMotion: true }) });
		if (!ana || !ben || !cleo) throw new Error('three players expected');
		await ana.playing.fillAll(answersLeaving(letter, ana, [5]));
		await ben.playing.fillAll(answersLeaving(letter, ben));
		await cleo.playing.fillAll(answersLeaving(letter, cleo));
		await expectServerHas(ben, ana, 5, 6);

		await ana.playing.openAskDialog();
		await expectAccessible(ana, 'confirmation to ask everyone to stop');
		await ana.playing.askConfirm.click();

		await expect(ben.playing.voteTitle('Ana')).toBeVisible();
		await expectAccessible(ben, 'voter prompt');
		await expectAccessible(ana, 'asker banner');
		await ben.playing.voteYes.click();
		await expect(ben.playing.voteWaiting(1, 2)).toBeVisible();
		await expectAccessible(ben, 'voter prompt after a yes');
	});
}

/**
 * The screens of a session that ended, the invite gate when there is nothing to join, and the offline
 * banner over a screen whose connection dropped (the "Reconectando…" pill hides while the browser is
 * offline and shows for a moment only once it is back: it is covered by the playground sweep).
 */
for (const locale of LOCALES) {
	test(`no accessibility violations on the closed screens, the blocked gate and the offline banner (${locale})`, async ({
		app
	}) => {
		const ana = await app.player('Ana', { locale, reducedMotion: true });
		const beto = await app.player('Beto', { locale, reducedMotion: true, network: true });
		const dani = await app.player('Dani', { locale, reducedMotion: true });
		app.guard.allow({
			kind: 'response',
			url: /\/api\/rooms\/[A-Z0-9]{4}$/,
			status: 404,
			why: 'looking up a room that does not exist'
		});

		await test.step('an invite link to a room that does not exist', async () => {
			await dani.page.goto('/W2W2');
			await dani.gate.expectBlocked('not_found');
			await expectAccessible(dani, 'gate (no such room)');
		});

		const code = await createRoom(app, ana);
		await joinByCode(beto, code);
		await ana.lobby.expectPlayers(['Ana', 'Beto']);

		await test.step('the connection drops: the offline banner over the lobby', async () => {
			await beto.network?.drop();
			await expect(beto.offlineBanner).toBeVisible();
			await expectAccessible(beto, 'lobby with the offline banner');
			await beto.network?.restore();
			await expect(beto.offlineBanner).toHaveCount(0);
			await expect(beto.reconnectingBanner).toHaveCount(0);
		});

		await test.step('a second tab of the same player replaces the first', async () => {
			const second = await ana.newTab();
			await second.goto(`/${code}`);
			await second.getByRole('button', { name: ana.t('gate_join'), exact: true }).click();
			await expect(
				ana.page.getByRole('heading', { name: ana.t('closed_replaced_title') })
			).toBeVisible();
			await expectAccessible(ana, 'closed (replaced by another tab)');
			await second.close();
		});

		await test.step('the host removes a player', async () => {
			await ana.page
				.getByRole('button', { name: ana.t('closed_replaced_action'), exact: true })
				.click();
			await ana.lobby.expectVisible(code);
			await ana.lobby.kick('Beto');
			await beto.closed.expect('kicked', beto.t('closed_kicked_text'));
			await expectAccessible(beto, 'closed (removed from the room)');
		});
	});
}

/**
 * The account screens. Google sign-in cannot run in the suite, so `/api/session`, `/api/me` and
 * `/api/me/stats` are answered by the test (the service worker is kept out, which would hide the calls
 * from `page.route`).
 */
type Account = { loggedIn: boolean; stats?: object | null };

async function fakeAccount(player: Player, account: Account): Promise<void> {
	const json = (body: object) => (route: { fulfill: (o: { json: object }) => Promise<void> }) =>
		route.fulfill({ json: body });
	await player.page.route(
		'**/api/session',
		json({ guest: true, googleEnabled: true, loggedIn: account.loggedIn })
	);
	if (!account.loggedIn) return;
	await player.page.route(
		'**/api/me',
		json({
			id: '3b1f6f0a-0000-4000-8000-000000000000',
			name: player.name,
			profile: { nickname: player.name, avatar: { emoji: '🍓', color: 'strawberry' } }
		})
	);
	await player.page.route('**/api/me/stats', json(account.stats ?? {}));
}

// 12345 points: "12.345" in es and nl, "12,345" in en (Spanish writes four-digit numbers without a separator)
const STATS = { gamesPlayed: 12, wins: 5, totalPoints: 12345, bestRound: 95, uniqueAnswers: 41 };

for (const locale of LOCALES) {
	test(`no accessibility violations on the account screens (${locale})`, async ({ app }) => {
		const options = { locale, reducedMotion: true, serviceWorkers: 'block' } as const;

		await test.step('a guest on the stats screen is invited to sign in', async () => {
			const guest = await app.player('Gaby', options);
			await fakeAccount(guest, { loggedIn: false });
			await guest.page.goto('/stats');
			await expect(
				guest.page.getByRole('heading', { name: guest.t('stats_login_title'), exact: true })
			).toBeVisible();
			await expectAccessible(guest, 'stats (guest)');
		});

		await test.step('a signed-in player sees Home with the account row and the stats', async () => {
			const ana = await app.player('Ana', options);
			await fakeAccount(ana, { loggedIn: true, stats: STATS });
			await ana.home.open();
			await expect(ana.page.getByRole('link', { name: ana.t('account_stats_link') })).toBeVisible();
			await expectAccessible(ana, 'home (signed in)');

			await ana.page.getByRole('link', { name: ana.t('account_stats_link') }).click();
			await expect(ana.page.getByText(ana.t('stats_loaded'), { exact: true })).toBeAttached();
			await expect(ana.page.getByText(/^12[.,]345$/)).toBeVisible();
			await expectAccessible(ana, 'stats (numbers)');

			await ana.page.getByRole('button', { name: ana.t('stats_delete'), exact: true }).click();
			await expect(
				ana.page.getByRole('dialog', { name: ana.t('stats_delete_title'), exact: true })
			).toBeVisible();
			await expectAccessible(ana, 'delete-account dialog');
		});

		await test.step('a signed-in player who has not played yet', async () => {
			const eli = await app.player('Eli', options);
			await fakeAccount(eli, {
				loggedIn: true,
				stats: { gamesPlayed: 0, wins: 0, totalPoints: 0, bestRound: 0, uniqueAnswers: 0 }
			});
			await eli.page.goto('/stats');
			await expect(
				eli.page.getByRole('heading', { name: eli.t('stats_empty_title'), exact: true })
			).toBeVisible();
			await expectAccessible(eli, 'stats (no games yet)');
		});
	});
}
