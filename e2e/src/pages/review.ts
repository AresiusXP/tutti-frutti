import { expect, type Locator } from '@playwright/test';
import { msgPattern } from '../messages';
import { PageObject } from './base';

export class ReviewPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: msgPattern(this.player.locale, 'review_label') });
	}

	async expectVisible(): Promise<void> {
		await expect(this.main).toBeVisible();
	}

	/** "País · 3/6": what everybody sees (the host moves it). */
	get title(): Locator {
		return this.page.getByTestId('category-title');
	}

	async expectCategory(label: string, n: number, total: number): Promise<void> {
		await expect(this.title).toHaveText(this.t('review_category_pos', { label, n, total }));
	}

	get prev(): Locator {
		return this.button('review_prev');
	}

	get next(): Locator {
		return this.button('review_next');
	}

	get finishButton(): Locator {
		return this.button('review_finish');
	}

	/** The host's category dots (1-based). */
	jump(n: number): Locator {
		return this.page.getByRole('button', {
			name: msgPattern(this.player.locale, 'review_jump', { n })
		});
	}

	hostControls(hostName: string): Locator {
		return this.page
			.getByRole('status')
			.filter({ hasText: this.t('review_host_controls', { name: hostName }) });
	}

	get spectatorNote(): Locator {
		return this.page.getByText(this.t('review_spectator'), { exact: true });
	}

	/** The card of one player's answer in the current category. */
	card(name: string): Locator {
		return this.page
			.locator('li[data-player]')
			.filter({ has: this.page.getByText(name, { exact: true }) });
	}

	answer(name: string): Locator {
		return this.card(name).getByTestId('answer');
	}

	badge(name: string): Locator {
		return this.card(name).getByTestId('points-badge');
	}

	async expectAnswer(name: string, text: string): Promise<void> {
		await expect(this.answer(name)).toHaveText(text);
	}

	/** The points pill of a card: 10 unique, 5 shared, 0 invalid. Empty answers have no pill. */
	async expectPoints(name: string, points: number): Promise<void> {
		await expect(this.badge(name)).toHaveAttribute('data-points', String(points));
	}

	async expectNoPoints(name: string): Promise<void> {
		await expect(this.badge(name)).toHaveCount(0);
	}

	rejectButton(name: string, tally?: { votes: number; needed: number }): Locator {
		return this.card(name).getByRole('button', {
			name: msgPattern(this.player.locale, 'review_reject_aria', { name, ...tally })
		});
	}

	/** Votes to reject `name`'s answer and waits for the button to show the vote as cast. */
	async reject(name: string): Promise<void> {
		await this.rejectButton(name).click();
		await expect(this.rejectButton(name)).toHaveAttribute('aria-pressed', 'true');
	}

	get rejectedChip(): Locator {
		return this.page.getByText(this.t('review_rejected'), { exact: true });
	}

	get keepReviewing(): Locator {
		return this.button('review_keep_reviewing');
	}

	/** "You haven't opened every category yet (1/6)": the host's confirmation before finishing early. */
	unseenWarning(seen: number, total: number): Locator {
		return this.page.getByText(this.t('review_finish_unseen', { seen, total }), { exact: true });
	}

	/** "Finish review". When categories were skipped the app asks first; `anyway` confirms. */
	async finish({ anyway = false }: { anyway?: boolean } = {}): Promise<void> {
		await this.finishButton.click();
		if (anyway) await this.button('review_finish_anyway').click();
	}
}

export class SummaryPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: msgPattern(this.player.locale, 'summary_label') });
	}

	async expectVisible(): Promise<void> {
		await expect(this.main).toBeVisible();
	}

	row(name: string): Locator {
		return this.page
			.getByRole('list', { name: this.t('summary_ranking'), exact: true })
			.getByRole('listitem')
			.filter({ has: this.page.getByText(name, { exact: true }) });
	}

	/** Running total of one player. */
	total(name: string): Locator {
		return this.row(name).getByTestId('total');
	}

	/** Points of this round ("+45") of one player. */
	delta(name: string): Locator {
		return this.row(name).getByTestId('delta');
	}

	async expectTotals(totals: Record<string, number>): Promise<void> {
		for (const [name, total] of Object.entries(totals)) {
			await expect(this.total(name), `${name}'s total`).toHaveText(String(total));
		}
	}

	async expectRoundPoints(points: Record<string, number>): Promise<void> {
		for (const [name, n] of Object.entries(points)) {
			await expect(this.delta(name), `${name}'s points this round`).toHaveText(
				new RegExp(`^\\+?${n}`)
			);
		}
	}

	/** Position (1-based, ties share it) of one player in the ranking. */
	async expectRank(name: string, rank: number): Promise<void> {
		await expect(this.row(name), `${name}'s rank`).toHaveAttribute('data-rank', String(rank));
	}

	get nextRoundButton(): Locator {
		return this.button('summary_next_round');
	}

	get endGameButton(): Locator {
		return this.button('summary_end_game');
	}

	get lastRoundNote(): Locator {
		return this.page.getByTestId('last-round');
	}

	get waiting(): Locator {
		return this.page.getByRole('status').filter({ hasText: this.t('summary_waiting') });
	}

	async nextRound(): Promise<void> {
		await this.nextRoundButton.click();
	}

	/** "End game" (asks "End the game now?" unless it is the last round). */
	async endGame({ confirm = true }: { confirm?: boolean } = {}): Promise<void> {
		await this.endGameButton.click();
		if (confirm && (await this.button('summary_end_confirm_yes').isVisible())) {
			await this.button('summary_end_confirm_yes').click();
		}
	}
}

export class FinalPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: this.t('final_label'), exact: true });
	}

	async expectVisible(): Promise<void> {
		await expect(this.main).toBeVisible();
	}

	get podium(): Locator {
		return this.page.getByTestId('podium');
	}

	place(n: number): Locator {
		return this.podium.locator(`li[data-place="${n}"]`);
	}

	get ranking(): Locator {
		return this.page.getByRole('list', { name: this.t('final_ranking'), exact: true });
	}

	row(name: string): Locator {
		return this.ranking
			.getByRole('listitem')
			.filter({ has: this.page.getByText(name, { exact: true }) });
	}

	async expectWinner(name: string): Promise<void> {
		await expect(
			this.page.getByText(this.t('final_winner', { name }), { exact: true })
		).toBeVisible();
	}

	async expectTotals(totals: Record<string, number>): Promise<void> {
		for (const [name, total] of Object.entries(totals)) {
			await expect(this.row(name).getByTestId('total'), `${name}'s final score`).toHaveText(
				String(total)
			);
		}
	}

	get rematchButton(): Locator {
		return this.button('final_rematch');
	}

	get homeButton(): Locator {
		return this.button('final_home');
	}

	get waitingForRematch(): Locator {
		return this.page.getByRole('status').filter({ hasText: this.t('final_waiting_rematch') });
	}
}
