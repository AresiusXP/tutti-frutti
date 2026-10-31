import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { expect, test, type App } from '../src/fixtures';
import { expectServerHas, joinByCode } from '../src/flows';
import type { AvatarChoice } from '../src/pages/home';
import type { Player } from '../src/player';
import { CLASSIC_CATEGORIES, LETTER_TICK_MS, playableLetters, type Lang } from '../src/rules';

/**
 * Google Play store screenshots: one short game per language (four players, one round, a chosen letter,
 * real words), photographed at seven moments on a 1080x1920 phone, as the Android app shows it (launched
 * like the app, so no support link). Run with `npm run store:screenshots` (playwright.store.config.ts);
 * the files land in android/store/screenshots/<lang>/.
 */

const OUT_DIR = path.resolve(
	path.dirname(fileURLToPath(import.meta.url)),
	'../../android/store/screenshots'
);

/** How the Android app opens the site: the first document of the tab has this referrer. */
const TWA_REFERRER = 'android-app://xyz.aresius.tuttifrutti';

interface Seat {
	name: string;
	avatar: AvatarChoice;
	/** One answer per classic category, in game order; `null` leaves the field empty. */
	answers: (string | null)[];
}

interface Cast {
	letter: string;
	/** Host first (the first chooser and the one who calls Stop). */
	seats: [Seat, Seat, Seat, Seat];
	/**
	 * The category shown on the review screenshot (index in game order): one with a shared answer. The
	 * answers give distinct totals (60, 55, 50, 45) so the podium has one winner.
	 */
	reviewCategory: number;
}

const CASTS: Record<Lang, Cast> = {
	es: {
		letter: 'M',
		reviewCategory: 2,
		seats: [
			{
				name: 'Sofi',
				avatar: { emoji: '🍓', color: 'strawberry' },
				answers: ['Martina', 'México', 'Mono', 'Milanesa', 'Marrón', 'Mercedes']
			},
			{
				name: 'Mateo',
				avatar: { emoji: '🍍', color: 'banana' },
				answers: ['Manuel', 'Marruecos', 'Mono', 'Mandarina', 'Magenta', 'Movistar']
			},
			{
				name: 'Lucía',
				avatar: { emoji: '🍇', color: 'grape' },
				answers: ['Micaela', 'Malta', 'Mariposa', 'Mostaza', 'Morado', 'Motorola']
			},
			{
				name: 'Tomás',
				avatar: { emoji: '🍉', color: 'lime' },
				answers: ['Marcos', 'Mongolia', 'Medusa', 'Mermelada', 'Marrón', null]
			}
		]
	},
	en: {
		letter: 'S',
		reviewCategory: 2,
		seats: [
			{
				name: 'Emma',
				avatar: { emoji: '🍓', color: 'strawberry' },
				answers: ['Sophie', 'Spain', 'Shark', 'Sushi', 'Silver', 'Samsung']
			},
			{
				name: 'Liam',
				avatar: { emoji: '🍊', color: 'orange' },
				answers: ['Sam', 'Sweden', 'Shark', 'Spaghetti', 'Scarlet', 'Sony']
			},
			{
				name: 'Olivia',
				avatar: { emoji: '🍇', color: 'grape' },
				answers: ['Sarah', 'Switzerland', 'Snake', 'Sandwich', 'Salmon', 'Spotify']
			},
			{
				name: 'Noah',
				avatar: { emoji: '🍍', color: 'lime' },
				answers: ['Steve', 'Senegal', 'Seal', 'Soup', 'Silver', null]
			}
		]
	},
	nl: {
		letter: 'B',
		reviewCategory: 2,
		seats: [
			{
				name: 'Sanne',
				avatar: { emoji: '🍊', color: 'orange' },
				answers: ['Bram', 'België', 'Beer', 'Bitterbal', 'Blauw', 'Bavaria']
			},
			{
				name: 'Daan',
				avatar: { emoji: '🍌', color: 'banana' },
				answers: ['Bas', 'Brazilië', 'Beer', 'Boerenkool', 'Bruin', 'Batavus']
			},
			{
				name: 'Fleur',
				avatar: { emoji: '🍓', color: 'strawberry' },
				answers: ['Britt', 'Bulgarije', 'Bever', 'Bami', 'Beige', 'Blokker']
			},
			{
				name: 'Joris',
				avatar: { emoji: '🍇', color: 'blueberry' },
				answers: ['Bart', 'Bolivia', 'Bij', 'Brood', 'Blauw', null]
			}
		]
	}
};

/** How many fields each guest has filled when the host's playing screen is photographed. */
const EARLY_FILLS = [4, 2, 3];

const MAX_ATTEMPTS = 4;

const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function launchLikeTheApp(page: Page): Promise<void> {
	await page.addInitScript((referrer) => {
		if (sessionStorage.getItem('store.launched')) return;
		sessionStorage.setItem('store.launched', '1');
		Object.defineProperty(document, 'referrer', { get: () => referrer, configurable: true });
	}, TWA_REFERRER);
}

async function shot(
	player: Player,
	lang: Lang,
	name: string,
	animations: 'allow' | 'disabled' = 'allow'
): Promise<void> {
	await player.page.evaluate(() => document.fonts.ready);
	await player.page.screenshot({
		path: path.join(OUT_DIR, lang, `${name}.png`),
		caret: 'hide',
		animations
	});
}

interface SplashWindow {
	__storeSplash?: Promise<void>;
}

/** Resolves ~420 ms after the Stop overlay appears: past its 350 ms entrance, before its 900 ms end. */
async function armSplash(page: Page): Promise<void> {
	await page.evaluate(() => {
		const w = window as unknown as SplashWindow;
		w.__storeSplash = new Promise<void>((resolve) => {
			const seen = () => document.querySelector('[data-testid="basta-splash"]') !== null;
			const observer = new MutationObserver(() => {
				if (!seen()) return;
				observer.disconnect();
				setTimeout(resolve, 420);
			});
			observer.observe(document.body, { childList: true, subtree: true });
		});
	});
}

async function splashSettled(page: Page): Promise<void> {
	await page.evaluate(() => (window as unknown as SplashWindow).__storeSplash);
}

/**
 * One attempt: a fresh table plays one round. Returns the letter the chooser hit; the screenshots after
 * the letter screen are only taken when it is the cast's letter (the answers are written for it).
 */
async function playOnce(app: App, lang: Lang, aimOffsetMs: number): Promise<string> {
	const cast = CASTS[lang];
	const categories = CLASSIC_CATEGORIES[lang];
	const players: Player[] = [];
	for (const seat of cast.seats) {
		const player = await app.player(seat.name, { locale: lang });
		await launchLikeTheApp(player.page);
		players.push(player);
	}
	const [host, ...guests] = players as [Player, Player, Player, Player];
	const [hostSeat, ...guestSeats] = cast.seats;

	await test.step('home', async () => {
		await host.home.open();
		await host.home.setNickname(hostSeat.name);
		await host.home.pickAvatar(hostSeat.avatar);
		await expect(host.home.supportLink).toHaveCount(0);
		await host.page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
		await shot(host, lang, '01-home');
	});

	const code = await test.step('lobby', async () => {
		await app.beforeRoomCreation();
		const code = await host.home.create();
		await host.lobby.expectVisible(code);
		for (const [i, guest] of guests.entries()) {
			await joinByCode(guest, code, guestSeats[i]?.avatar);
		}
		await host.lobby.expectPlayers(cast.seats.map((s) => s.name));
		await host.lobby.setRounds(1);
		await host.page.evaluate(() => window.scrollTo(0, 0));
		await shot(host, lang, '02-lobby');
		return code;
	});

	const letter = await test.step(`letter (aiming at ${cast.letter})`, async () => {
		const letters = playableLetters(lang, true);
		const slot = letters.indexOf(cast.letter);
		let aimMs = slot * LETTER_TICK_MS + LETTER_TICK_MS / 2 + aimOffsetMs;
		while (aimMs < 1500) aimMs += letters.length * LETTER_TICK_MS;
		await Promise.all(players.map((p) => p.letter.watchArrival()));
		await host.lobby.start();
		await Promise.all(players.map((p) => p.expectScreen('letter')));
		await expect(host.letter.stopButton, 'the host chooses the first letter').toBeVisible();
		await pause(300);
		// the spinning letter is a blur in a still: photograph it at rest
		await shot(host, lang, '03-letter', 'disabled');
		await host.letter.stopAfter(aimMs);
		await Promise.all(players.map((p) => p.expectScreen('playing')));
		return host.playing.letter();
	});

	test.info().annotations.push({ type: 'letter', description: `${lang} ${code}: ${letter}` });
	if (letter !== cast.letter) return letter;

	await test.step('playing', async () => {
		await Promise.all(
			guests.map((guest, i) => {
				const answers = guestSeats[i]?.answers ?? [];
				const early = EARLY_FILLS[i] ?? 0;
				return guest.playing.fillAll(answers.map((a, field) => (field < early ? a : null)));
			})
		);
		await host.playing.fillAll(hostSeat.answers);
		for (const [i, guest] of guests.entries()) {
			const filled = (guestSeats[i]?.answers ?? []).filter(
				(a, field) => a !== null && field < (EARLY_FILLS[i] ?? 0)
			).length;
			await expectServerHas(host, guest, filled, categories.length);
		}
		await expect(host.playing.bastaButton).toBeEnabled();
		// the big letter of the reveal (1.2 s) is gone
		await expect(host.page.getByTestId('letter-reveal')).toHaveCount(0);
		await host.page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
		await host.page.evaluate(() => window.scrollTo(0, 0));
		await shot(host, lang, '04-playing');
	});

	await test.step('Stop!', async () => {
		for (const [i, guest] of guests.entries()) {
			const answers = guestSeats[i]?.answers ?? [];
			const early = EARLY_FILLS[i] ?? 0;
			for (const [field, answer] of answers.entries()) {
				if (field >= early && answer !== null) {
					await guest.playing.fill(categories[field] as string, answer);
				}
			}
			const total = answers.filter((a) => a !== null).length;
			await expectServerHas(host, guest, total, categories.length);
		}
		const viewer = guests[1];
		await armSplash(viewer.page);
		await host.playing.bastaButton.click();
		await splashSettled(viewer.page);
		await viewer.page.screenshot({
			path: path.join(OUT_DIR, lang, '05-stop.png'),
			caret: 'hide'
		});
		await expect(viewer.playing.graceBanner).toBeVisible();
	});

	await test.step('review', async () => {
		await Promise.all(players.map((p) => p.expectScreen('review')));
		for (let i = 1; i <= cast.reviewCategory; i++) await host.review.next.click();
		await host.review.expectCategory(
			categories[cast.reviewCategory] as string,
			cast.reviewCategory + 1,
			categories.length
		);
		await pause(400);
		await shot(host, lang, '06-review');
		for (let i = cast.reviewCategory + 1; i < categories.length; i++) {
			await host.review.next.click();
		}
		await host.review.finish();
	});

	await test.step('final', async () => {
		await Promise.all(players.map((p) => p.expectScreen('summary')));
		await host.summary.endGame();
		await Promise.all(players.map((p) => p.expectScreen('final')));
		await expect(host.final.supportLink).toHaveCount(0);
		await pause(1500);
		await shot(host, lang, '07-final');
	});

	return letter;
}

for (const lang of ['es', 'en', 'nl'] as const) {
	test(`store screenshots (${lang})`, async ({ app }) => {
		const letters = playableLetters(lang, true);
		const target = CASTS[lang].letter;
		let offsetMs = -10; // the page sees the letter screen a few ms after the server started the round
		for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
			const letter = await playOnce(app, lang, offsetMs);
			if (letter === target) return;
			// a miss: how many slots off (the list is circular), aim again at a fresh table
			const half = letters.length / 2;
			let off = letters.indexOf(letter) - letters.indexOf(target);
			if (off > half) off -= letters.length;
			if (off < -half) off += letters.length;
			offsetMs -= off * LETTER_TICK_MS;
		}
		throw new Error(`no letter ${target} in ${MAX_ATTEMPTS} attempts`);
	});
}
