import { expect } from '@playwright/test';
import type { App } from './fixtures';
import type { Player, ScreenName } from './player';
import type { AvatarChoice } from './pages/home';

/** Letters only (a letter + a name + a letter), so it is a valid answer for any category. */
export function answerFor(letter: string, player: Player | string, field: number): string {
	const name = typeof player === 'string' ? player : player.name;
	return `${letter}${name.toLowerCase().replace(/[^a-z]/g, '')}${'abcdefghij'[field] ?? 'z'}`;
}

/** `count` unique answers of one player, e.g. `Manaa, Manab, Manac`. */
export function uniqueAnswers(letter: string, player: Player | string, count: number): string[] {
	return Array.from({ length: count }, (_, i) => answerFor(letter, player, i));
}

/** The host opens Home, picks a nickname (and avatar) and creates a room. Returns its code. */
export async function createRoom(app: App, host: Player, avatar?: AvatarChoice): Promise<string> {
	await host.home.open();
	await host.home.setNickname(host.name);
	if (avatar) await host.home.pickAvatar(avatar);
	await app.beforeRoomCreation();
	const code = await host.home.create();
	await host.lobby.expectVisible(code);
	return code;
}

/** Joins from Home: nickname, the code in the four boxes, "Join game". */
export async function joinByCode(
	player: Player,
	code: string,
	avatar?: AvatarChoice
): Promise<void> {
	await player.home.open();
	await player.home.setNickname(player.name);
	if (avatar) await player.home.pickAvatar(avatar);
	await player.home.submitCode(code);
	await player.lobby.expectVisible(code);
}

/** Opens the invite link (`/kt7p`, any case) in a fresh tab: gate, nickname, "Join". */
export async function joinByInviteLink(
	player: Player,
	code: string,
	{ lowercase = true, landing = 'lobby' }: { lowercase?: boolean; landing?: ScreenName } = {}
): Promise<void> {
	await player.page.goto(`/${lowercase ? code.toLowerCase() : code}`);
	await player.gate.expectOpen(code);
	await expect
		.poll(() => new URL(player.page.url()).pathname, { message: 'the link was canonicalised' })
		.toBe(`/${code}`);
	await player.gate.setNickname(player.name);
	await player.gate.join();
	await player.expectScreen(landing);
	if (landing === 'lobby') await player.lobby.expectVisible(code);
}

/** The host starts the game; everybody lands on the letter screen. */
export async function startGame(host: Player, everyone: Player[]): Promise<void> {
	await host.lobby.start();
	await Promise.all(everyone.map((p) => p.expectScreen('letter')));
}

/**
 * Whoever is the chooser taps Stop (the others have no such button) and everybody reaches the
 * playing screen. `delayMs` waits before tapping: the server derives the letter from the elapsed
 * time, so a tap right away always yields the first letters.
 */
export async function chooserStops(
	everyone: Player[],
	{ delayMs = 0 }: { delayMs?: number } = {}
): Promise<Player> {
	await expect
		.poll(
			async () =>
				(await Promise.all(everyone.map((p) => p.letter.stopButton.isVisible()))).filter(Boolean)
					.length,
			{ message: 'exactly one player is the chooser' }
		)
		.toBe(1);
	const flags = await Promise.all(everyone.map((p) => p.letter.stopButton.isVisible()));
	const chooser = everyone[flags.indexOf(true)];
	if (!chooser) throw new Error('nobody can stop the letter');
	if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
	await chooser.letter.stop();
	await Promise.all(everyone.map((p) => p.expectScreen('playing')));
	return chooser;
}

/** Types one answer per field, in field order (`null` leaves a field empty) and waits for each to show. */
export async function fillAnswers(player: Player, answers: (string | null)[]): Promise<void> {
	await player.playing.fillAll(answers);
}

/** Waits until `viewer` sees that the server has `filled` answers of `other` (the strip "Beto: 5 of 6"). */
export async function expectServerHas(
	viewer: Player,
	other: Player,
	filled: number,
	total: number
): Promise<void> {
	await expect(viewer.playing.progress(other.name, filled, total)).toBeAttached();
}

/**
 * The caller presses Basta once every field is valid. The others record the overlay before it
 * appears (it lasts under a second) and end up in the grace countdown.
 */
export async function bastaBy(caller: Player, others: Player[]): Promise<void> {
	await Promise.all(others.map((p) => p.playing.watchSplash()));
	await expect(caller.playing.bastaButton).toBeEnabled();
	await caller.playing.bastaButton.click();
	await Promise.all(others.map((p) => expect(p.playing.graceBanner).toBeVisible()));
}

/**
 * A fast round for tests that only need to get through it: the chooser stops, the caller writes
 * unique answers in every field and calls Basta, the host skips through the review and
 * lands on the summary. Returns the letter played.
 */
export async function quickRound(opts: {
	host: Player;
	everyone: Player[];
	caller: Player;
	fields: number;
	delayMs?: number;
}): Promise<{ letter: string; round: number; chooser: Player }> {
	const { host, everyone, caller, fields, delayMs } = opts;
	const chooser = await chooserStops(everyone, { delayMs });
	const { letter, round } = await caller.playing.roundInfo();
	await fillAnswers(caller, uniqueAnswers(letter, caller, fields));
	const others = everyone.filter((p) => p !== caller);
	await bastaBy(caller, others);
	await Promise.all(everyone.map((p) => p.expectScreen('review')));
	await host.review.finish({ anyway: true });
	await Promise.all(everyone.map((p) => p.expectScreen('summary')));
	return { letter, round, chooser };
}
