import { expect } from '@playwright/test';
import type { App } from './fixtures';
import type { Locale } from './messages';
import type { Player, PlayerOptions, ScreenName } from './player';
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

/** Unique answers for every field except the ones in `skip`, which stay empty (`null`). */
export function answersLeaving(
	letter: string,
	player: Player | string,
	skip: number[] = [],
	fields = 6
): (string | null)[] {
	return uniqueAnswers(letter, player, fields).map((answer, i) =>
		skip.includes(i) ? null : answer
	);
}

/** The host opens Home, picks a nickname (and avatar) and creates a room. Returns its code. */
export async function createRoom(
	app: App,
	host: Player,
	avatar?: AvatarChoice,
	visibility?: 'private' | 'public'
): Promise<string> {
	await host.home.open();
	await host.home.setNickname(host.name);
	if (avatar) await host.home.pickAvatar(avatar);
	if (visibility) await host.home.setVisibility(visibility);
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
	const chooser = await findChooser(everyone);
	if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
	await chooser.letter.stop();
	await Promise.all(everyone.map((p) => p.expectScreen('playing')));
	return chooser;
}

async function findChooser(everyone: Player[]): Promise<Player> {
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
	return chooser;
}

/**
 * The chooser taps Stop `afterMs` after the letter screen appeared on THEIR phone, timed inside the
 * page (`everyone` must have called `letter.watchArrival()` before the round started). That is how a
 * test aims at one letter: the server shows `letters[floor(elapsed / 120 ms) mod n]`. Leave at least
 * ~700 ms: finding the chooser takes a moment.
 */
export async function chooserStopsAfter(everyone: Player[], afterMs: number): Promise<Player> {
	const chooser = await findChooser(everyone);
	await chooser.letter.stopAfter(afterMs);
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
 * Somebody with answers missing asks everyone to stop: the main button, the confirmation, "Yes, ask
 * everyone". The asker gets the banner and every voter the (non-modal) prompt with the asker's name.
 */
export async function askToStop(asker: Player, voters: Player[]): Promise<void> {
	await asker.playing.askEveryoneToStop();
	await Promise.all(voters.map((v) => expect(v.playing.voteTitle(asker.name)).toBeVisible()));
}

export interface OpenRoundOptions {
	locale?: Locale;
	/** The first one is the host (and the first chooser). Default: Ana, Ben, Cleo. */
	names?: string[];
	playerOptions?: (name: string) => PlayerOptions;
	/** The host's turn in the lobby (settings) while everybody is there, before the game starts. */
	lobby?: (host: Player, guests: Player[]) => Promise<void>;
}

/**
 * A table ready to play: everybody joins the host's room by code, the host starts the game and stops the
 * letter. Everybody is on the playing screen of round 1; `letter` is the round's letter.
 */
export async function openRound(
	app: App,
	options: OpenRoundOptions = {}
): Promise<{ players: Player[]; host: Player; code: string; letter: string }> {
	const { locale = 'es', names = ['Ana', 'Ben', 'Cleo'] } = options;
	const players: Player[] = [];
	for (const name of names) {
		players.push(await app.player(name, { locale, ...options.playerOptions?.(name) }));
	}
	const [host, ...guests] = players as [Player, ...Player[]];
	const code = await createRoom(app, host);
	for (const guest of guests) await joinByCode(guest, code);
	await host.lobby.expectPlayers(names);
	await options.lobby?.(host, guests);
	await startGame(host, players);
	await chooserStops(players);
	return { players, host, code, letter: await host.playing.letter() };
}

/**
 * The rest of a fast round for tests that only need to get through it: the caller writes unique
 * answers in every field and calls Basta, the host skips through the review and lands on the
 * summary. Starts on the playing screen. Returns the letter played.
 */
export async function playOutRound(opts: {
	host: Player;
	everyone: Player[];
	caller: Player;
	fields: number;
}): Promise<{ letter: string; round: number }> {
	const { host, everyone, caller, fields } = opts;
	const { letter, round } = await caller.playing.roundInfo();
	await fillAnswers(caller, uniqueAnswers(letter, caller, fields));
	const others = everyone.filter((p) => p !== caller);
	await bastaBy(caller, others);
	await Promise.all(everyone.map((p) => p.expectScreen('review')));
	await host.review.finish({ anyway: true });
	await Promise.all(everyone.map((p) => p.expectScreen('summary')));
	return { letter, round };
}

/** A whole fast round: the chooser stops (after `delayMs`), then `playOutRound`. */
export async function quickRound(opts: {
	host: Player;
	everyone: Player[];
	caller: Player;
	fields: number;
	delayMs?: number;
}): Promise<{ letter: string; round: number; chooser: Player }> {
	const chooser = await chooserStops(opts.everyone, { delayMs: opts.delayMs });
	return { ...(await playOutRound(opts)), chooser };
}
