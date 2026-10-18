import { expect, type Locator } from '@playwright/test';
import { msgPattern } from '../messages';
import { PageObject } from './base';

export class LetterPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: this.t('play_letter_screen'), exact: true });
	}

	/** The chooser's big button; nobody else has one. */
	get stopButton(): Locator {
		return this.button('letter_stop');
	}

	choosing(name: string): Locator {
		return this.page
			.getByRole('status')
			.filter({ hasText: this.t('play_letter_choosing', { name }) });
	}

	get joinsNextRound(): Locator {
		return this.page.getByText(this.t('play_joins_next_round'), { exact: true });
	}

	async expectVisible(): Promise<void> {
		await expect(this.main).toBeVisible();
	}

	async stop(): Promise<void> {
		await this.stopButton.click();
	}

	/** The letters already played, as the chooser sees them. */
	get usedLetters(): Locator {
		return this.page
			.getByRole('list', { name: this.t('play_used_letters'), exact: true })
			.getByRole('listitem');
	}

	/**
	 * Records, on the page's own clock, when the letter screen shows up. Call BEFORE the screen can
	 * appear (before the host starts the game / the next round), then `stopAfter`.
	 */
	async watchArrival(): Promise<void> {
		await this.page.evaluate((label) => {
			const w = window as unknown as LetterClockWindow;
			w.__tfLetterObserver?.disconnect();
			w.__tfLetterAt = undefined;
			const check = () => {
				if (w.__tfLetterAt !== undefined) return;
				const shown = [...document.querySelectorAll('main')].some(
					(main) => main.getAttribute('aria-label') === label
				);
				if (shown) w.__tfLetterAt = performance.now();
			};
			w.__tfLetterObserver = new MutationObserver(check);
			w.__tfLetterObserver.observe(document.documentElement, {
				childList: true,
				subtree: true,
				attributes: true,
				attributeFilter: ['aria-label']
			});
			check();
		}, this.t('play_letter_screen'));
	}

	/**
	 * Taps Stop `afterMs` after the letter screen appeared (see `watchArrival`). The wait and the tap
	 * run inside the page, so the test's own scheduling cannot add jitter: the server derives the letter
	 * from the time between the start of the round and the tap, and every letter lasts only 120 ms.
	 */
	async stopAfter(afterMs: number): Promise<void> {
		await this.page.evaluate(
			({ afterMs, stop }) =>
				new Promise<void>((resolve, reject) => {
					const w = window as unknown as LetterClockWindow;
					const shownAt = w.__tfLetterAt;
					if (shownAt === undefined) return reject(new Error('watchArrival() was not called'));
					const button = [...document.querySelectorAll('button')].find(
						(b) => b.textContent?.trim() === stop
					);
					if (!button) return reject(new Error(`no "${stop}" button on the screen`));
					const due = shownAt + afterMs;
					const tap = () => {
						while (performance.now() < due) {
							// spin the last few milliseconds: timers are not that exact
						}
						button.click();
						resolve();
					};
					const early = due - performance.now() - 5;
					if (early > 0) setTimeout(tap, early);
					else tap();
				}),
			{ afterMs, stop: this.t('letter_stop') }
		);
	}
}

interface LetterClockWindow {
	__tfLetterAt?: number;
	__tfLetterObserver?: MutationObserver;
}

interface Timeout {
	timeout?: number;
}

interface SplashWindow {
	__tfSplash?: string[][];
}

/** What `watchGrace()` records: every text the grace banner showed and whether an input was locked meanwhile. */
export interface GraceRecord {
	texts: string[];
	inputLocked: boolean;
}

interface GraceWindow {
	__tfGrace?: GraceRecord;
}

export class PlayingPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', {
			name: msgPattern(this.player.locale, 'play_screen_label')
		});
	}

	async expectVisible(): Promise<void> {
		await expect(this.main).toBeVisible();
	}

	/** `{ round, letter }` read from the screen's accessible name ("Ronda 1: letra M"). */
	async roundInfo(): Promise<{ round: number; letter: string }> {
		await this.expectVisible();
		const label = (await this.main.getAttribute('aria-label')) ?? '';
		const found = msgPattern(this.player.locale, 'play_screen_label').exec(label);
		if (!found) throw new Error(`cannot read the round from "${label}"`);
		return { round: Number(found[1]), letter: found[2] as string };
	}

	async letter(): Promise<string> {
		return (await this.roundInfo()).letter;
	}

	get fields(): Locator {
		return this.page
			.getByRole('group', { name: this.t('play_answers_label'), exact: true })
			.getByRole('textbox');
	}

	field(label: string): Locator {
		return this.page.getByRole('textbox', { name: label, exact: true });
	}

	async fill(label: string, text: string): Promise<void> {
		await this.field(label).fill(text);
		await expect(this.field(label)).toHaveValue(text);
	}

	/** Writes one answer per field, in field order. `null` leaves a field empty. */
	async fillAll(answers: (string | null)[]): Promise<void> {
		await expect(this.fields).toHaveCount(answers.length);
		for (const [i, text] of answers.entries()) {
			if (text === null) continue;
			const field = this.fields.nth(i);
			await field.fill(text);
			await expect(field).toHaveValue(text);
		}
	}

	async expectValues(values: string[]): Promise<void> {
		await expect(this.fields).toHaveCount(values.length);
		for (const [i, text] of values.entries()) {
			await expect(this.fields.nth(i)).toHaveValue(text);
		}
	}

	get bastaButton(): Locator {
		return this.button('basta_button');
	}

	/** The line above the main button ("Te faltan 2 · tocá para pedir que paren", "¡Todo listo!"). */
	hint(text: string): Locator {
		return this.page.locator('#basta-hint').filter({ hasText: text });
	}

	// ---- vote to stop -----------------------------------------------------------------------

	/** The confirmation before asking: an `alertdialog` named "¿Pedir que paren?". */
	get askDialog(): Locator {
		return this.page.getByRole('alertdialog', { name: this.t('play_ask_title'), exact: true });
	}

	get askConfirm(): Locator {
		return this.askDialog.getByRole('button', { name: this.t('play_ask_confirm'), exact: true });
	}

	get askKeep(): Locator {
		return this.askDialog.getByRole('button', { name: this.t('play_ask_keep'), exact: true });
	}

	/** Taps the main button with answers missing and waits for the confirmation. */
	async openAskDialog(): Promise<void> {
		await this.bastaButton.click();
		await expect(this.askDialog).toBeVisible();
	}

	/** Taps the main button, confirms, and waits for the asker's banner. */
	async askEveryoneToStop(): Promise<void> {
		await this.openAskDialog();
		await this.askConfirm.click();
		await expect(this.waitBanner).toBeVisible();
	}

	/** The asker's banner: "Esperando a los demás", "0 de 2 dijeron que sí", the countdown. */
	get waitBanner(): Locator {
		return this.page.getByTestId('stop-wait-banner');
	}

	/** "Esperando a los demás" + "1 de 2 dijeron que sí" in the asker's banner. */
	async expectTally(yes: number, total: number, options: Timeout = {}): Promise<void> {
		await expect(this.waitBanner).toContainText(this.t('play_ask_waiting'), options);
		await expect(this.waitBanner).toContainText(this.t('play_ask_tally', { yes, total }), options);
	}

	/** The countdown of a vote ("15" ... "0") in the asker's banner or the voters' prompt. */
	get voteTimer(): Locator {
		return this.page.getByRole('timer', { name: this.t('play_vote_time_label'), exact: true });
	}

	/** Seconds left on the vote's countdown. */
	async voteSecondsLeft(): Promise<number> {
		return Number(await this.voteTimer.innerText());
	}

	/** The voters' prompt (NOT a dialog): "Ana pide parar. ¿Parar?" with yes / no. */
	get votePrompt(): Locator {
		return this.page.getByTestId('stop-vote-sheet');
	}

	voteTitle(askerName: string): Locator {
		return this.votePrompt.getByText(this.t('play_vote_title', { name: askerName }), {
			exact: true
		});
	}

	get voteYes(): Locator {
		return this.votePrompt.getByRole('button', { name: this.t('play_vote_yes'), exact: true });
	}

	get voteNo(): Locator {
		return this.votePrompt.getByRole('button', { name: this.t('play_vote_no'), exact: true });
	}

	/** After a yes the prompt shrinks to "Esperando a los demás (1/2)". */
	voteWaiting(yes: number, total: number): Locator {
		return this.votePrompt.getByText(this.t('play_vote_waiting', { yes, total }), {
			exact: true
		});
	}

	/** The one-line note of spectators (and of anyone outside the electorate): "Ana pidió parar…". */
	onlooker(askerName: string): Locator {
		return this.page
			.getByTestId('stop-vote-onlooker')
			.filter({ hasText: this.t('play_vote_onlooker', { name: askerName }) });
	}

	/** A toast of the screen ("No todos aceptaron", "Se acabó el tiempo"): they live in a polite live region. */
	toast(text: string): Locator {
		return this.page.getByText(text, { exact: true });
	}

	/** Nobody is voting from this player's point of view: no banner, no prompt, no onlooker line. */
	async expectNoVote(options: Timeout = {}): Promise<void> {
		await expect(this.waitBanner).toHaveCount(0, options);
		await expect(this.votePrompt).toHaveCount(0, options);
		await expect(this.page.getByTestId('stop-vote-onlooker')).toHaveCount(0, options);
	}

	/** "Name: 3 of 6" in the progress strip (the server has that many answers of that player). */
	progress(name: string, filled: number, total: number): Locator {
		return this.page
			.getByRole('list', { name: this.t('play_progress_label'), exact: true })
			.getByText(this.t('play_progress', { name, filled, total }), { exact: true });
	}

	/** The m:ss clock of a round with a time limit. */
	get timer(): Locator {
		return this.page.getByRole('timer', { name: this.t('play_time_left'), exact: true });
	}

	/** The grace banner ("Ana cantó ¡BASTA!" / "¡Se acabó el tiempo!") with its 3-2-1. */
	get graceBanner(): Locator {
		return this.page.getByTestId('grace-banner');
	}

	/** The polite/assertive announcement region the screen reader hears ("Ana: ¡Basta para mí, ...!"). */
	get announcement(): Locator {
		return this.page.getByRole('alert');
	}

	get spectatorNote(): Locator {
		return this.page.getByText(this.t('play_spectator'), { exact: true });
	}

	/**
	 * The full-screen Basta overlay lasts under a second: record it with a MutationObserver so
	 * the assertion can never miss it. Call BEFORE the Basta; read with `splashes()`.
	 */
	async watchSplash(): Promise<void> {
		await this.page.evaluate(() => {
			const w = window as unknown as SplashWindow;
			w.__tfSplash = [];
			const grab = () => {
				for (const el of document.querySelectorAll('[data-testid="basta-splash"]')) {
					const texts = [...el.querySelectorAll('p')].map((p) => p.textContent?.trim() ?? '');
					if (!w.__tfSplash?.some((seen) => seen.join('|') === texts.join('|')))
						w.__tfSplash?.push(texts);
				}
			};
			new MutationObserver(grab).observe(document.body, { childList: true, subtree: true });
			grab();
		});
	}

	/** Every overlay shown since `watchSplash()`: the paragraphs of each (caller name, call). */
	splashes(): Promise<string[][]> {
		return this.page.evaluate(() => (window as unknown as SplashWindow).__tfSplash ?? []);
	}

	/**
	 * After a time-up the grace phase lasts only the 500 ms late-update allowance, shorter than two
	 * sequential assertions can be trusted to span: record the banner texts (and whether an answer
	 * field was locked while it was up) with a MutationObserver. Call BEFORE the deadline; read with
	 * `graces()`.
	 */
	async watchGrace(): Promise<void> {
		await this.page.evaluate(() => {
			const w = window as unknown as GraceWindow;
			const record: GraceRecord = { texts: [], inputLocked: false };
			w.__tfGrace = record;
			const grab = () => {
				for (const el of document.querySelectorAll('[data-testid="grace-banner"]')) {
					const text = el.textContent?.trim() ?? '';
					if (text && !record.texts.includes(text)) record.texts.push(text);
					if (document.querySelector('input:disabled')) record.inputLocked = true;
				}
			};
			new MutationObserver(grab).observe(document.body, {
				childList: true,
				subtree: true,
				characterData: true,
				attributes: true
			});
			grab();
		});
	}

	/** Everything the grace banner showed since `watchGrace()`. */
	graces(): Promise<GraceRecord> {
		return this.page.evaluate(
			() =>
				(window as unknown as GraceWindow).__tfGrace ?? {
					texts: [],
					inputLocked: false
				}
		);
	}

	async expectSplash(texts: string[]): Promise<void> {
		await expect
			.poll(() => this.splashes(), { message: 'the Basta overlay was shown' })
			.toContainEqual(texts);
	}
}
