import { expect, type Locator } from '@playwright/test';
import type { Lang } from '../rules';
import { PageObject } from './base';

const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const nameInRow = (name: string) => new RegExp(`(^|\\s)${escape(name)}(\\s|$)`);

export class LobbyPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: this.t('lobby_screen_label'), exact: true });
	}

	/** The big copyable code: its name starts with the visible code ("KT7P: tocá para copiar"). */
	codeButton(code: string): Locator {
		return this.button('lobby_copy_code', { code });
	}

	async expectVisible(code?: string): Promise<void> {
		await expect(this.main).toBeVisible();
		if (code) await expect(this.codeButton(code)).toHaveText(code);
	}

	/** The "Show the QR code" button next to Share. */
	get qrButton(): Locator {
		return this.button('lobby_qr_open');
	}

	get qrDialog(): Locator {
		return this.page.getByRole('dialog', { name: this.t('lobby_qr_title'), exact: true });
	}

	/** The QR sheet with the picture drawn (it is generated after the sheet opens). */
	async expectQrDrawn(code: string): Promise<void> {
		await expect(
			this.qrDialog.getByRole('img', { name: this.t('lobby_qr_alt', { code }), exact: true })
		).toBeVisible();
	}

	async closeQr(): Promise<void> {
		// the sheet has a ✕ in its header and a Close button in its footer, both named "Close": use the footer's
		await this.qrDialog
			.getByRole('button', { name: this.t('lobby_close'), exact: true })
			.last()
			.click();
		await expect(this.qrDialog).toBeHidden();
	}

	get leaveDialog(): Locator {
		return this.page.getByRole('dialog', { name: this.t('lobby_leave_title'), exact: true });
	}

	/** "Salir": opens the confirmation sheet (`leave()` goes on to confirm). */
	async openLeave(): Promise<void> {
		await this.button('lobby_leave').click();
		await expect(this.leaveDialog).toBeVisible();
	}

	async stay(): Promise<void> {
		await this.leaveDialog
			.getByRole('button', { name: this.t('lobby_leave_stay'), exact: true })
			.click();
		await expect(this.leaveDialog).toBeHidden();
	}

	/** Host only: the ✕ of a player's row, which turns the row into "Yes, remove X / Cancel". */
	async askKick(name: string): Promise<void> {
		await this.button('lobby_kick', { name }).click();
		await expect(this.button('lobby_kick_confirm', { name })).toBeVisible();
	}

	async cancelKick(name: string): Promise<void> {
		await this.button('lobby_cancel').click();
		await expect(this.button('lobby_kick_confirm', { name })).toHaveCount(0);
	}

	// ---- players ----------------------------------------------------------------------------

	get playerRegion(): Locator {
		return this.page.getByRole('region', { name: this.t('lobby_players_title'), exact: true });
	}

	get playerItems(): Locator {
		return this.playerRegion.getByRole('listitem');
	}

	/** The row text is "<avatar> <name> 👑 you": the name is a whole word of it. */
	playerRow(name: string): Locator {
		return this.playerItems.filter({ hasText: nameInRow(name) });
	}

	/** Exactly these nicknames, in this (join) order. */
	async expectPlayers(names: string[]): Promise<void> {
		await expect(this.playerItems).toHaveText(names.map(nameInRow));
	}

	async expectHost(name: string): Promise<void> {
		await expect(
			this.playerRow(name).getByRole('img', { name: this.t('lobby_host_badge'), exact: true })
		).toBeVisible();
	}

	async expectOffline(name: string, offline = true): Promise<void> {
		const mark = this.playerRow(name).getByRole('img', {
			name: this.t('lobby_offline'),
			exact: true
		});
		if (offline) await expect(mark).toBeVisible();
		else await expect(mark).toHaveCount(0);
	}

	/** Host only: removes a player (asks for confirmation first). */
	async kick(name: string): Promise<void> {
		await this.button('lobby_kick', { name }).click();
		await this.button('lobby_kick_confirm', { name }).click();
	}

	// ---- settings: host ---------------------------------------------------------------------

	get settings(): Locator {
		return this.page.getByRole('region', { name: this.t('lobby_settings_title'), exact: true });
	}

	async setLang(lang: Lang): Promise<void> {
		const group = this.settings.getByRole('group', {
			name: this.t('lobby_lang_label'),
			exact: true
		});
		const option = group.getByRole('button', { name: this.t(`lobby_lang_${lang}`), exact: true });
		await option.click();
		await expect(option).toHaveAttribute('aria-pressed', 'true');
	}

	get hardLetters(): Locator {
		return this.settings.getByRole('switch', { name: this.t('lobby_hard_label'), exact: true });
	}

	async setHardLetters(on: boolean): Promise<void> {
		if ((await this.hardLetters.getAttribute('aria-checked')) !== String(on)) {
			await this.hardLetters.click();
		}
		await expect(this.hardLetters).toHaveAttribute('aria-checked', String(on));
	}

	/** The host's "Permitir pedir que paren" switch (vote to stop). */
	get stopVote(): Locator {
		return this.settings.getByRole('switch', { name: this.t('lobby_stop_label'), exact: true });
	}

	async setStopVote(on: boolean): Promise<void> {
		if ((await this.stopVote.getAttribute('aria-checked')) !== String(on)) {
			await this.stopVote.click();
		}
		await expect(this.stopVote).toHaveAttribute('aria-checked', String(on));
	}

	/** What the hard-letters row says ("Fuera: K Ñ Q W X Y Z · se juegan 20 letras"). */
	get hardLettersDescription(): Locator {
		return this.settings.locator('#hard-desc');
	}

	roundsValue(n: number): Locator {
		return this.settings.getByRole('status', {
			name: this.t('lobby_rounds_value', { n }),
			exact: true
		});
	}

	/** Steps the rounds counter one by one until it shows `n`. */
	async setRounds(n: number): Promise<void> {
		const less = this.settings.getByRole('button', {
			name: this.t('lobby_rounds_less'),
			exact: true
		});
		const more = this.settings.getByRole('button', {
			name: this.t('lobby_rounds_more'),
			exact: true
		});
		const shown = this.settings.locator('output');
		for (let guard = 0; guard < 40; guard++) {
			const current = Number(await shown.innerText());
			if (current === n) break;
			await (current > n ? less : more).click();
			await expect(shown).not.toHaveText(String(current));
		}
		await expect(this.roundsValue(n)).toBeVisible();
	}

	roundsMax(max: number): Locator {
		return this.settings.getByText(this.t('lobby_rounds_max', { max }), { exact: true });
	}

	timeButton(seconds: number): Locator {
		const group = this.settings.getByRole('group', {
			name: this.t('lobby_time_label'),
			exact: true
		});
		// named by what it shows ("Libre", "60 s"): a different spoken name breaks WCAG 2.5.3
		const name =
			seconds === 0 ? this.t('lobby_time_off') : this.t('lobby_time_secs', { n: seconds });
		return group.getByRole('button', { name, exact: true });
	}

	async setTimeLimit(seconds: number): Promise<void> {
		await this.timeButton(seconds).click();
		await expect(this.timeButton(seconds)).toHaveAttribute('aria-pressed', 'true');
	}

	// ---- settings: what everybody sees ------------------------------------------------------

	/** The read-only chips guests see: language, hard letters, rounds, time, vote to stop. */
	get chips(): Locator {
		return this.page
			.getByRole('list', { name: this.t('lobby_settings_title'), exact: true })
			.getByRole('listitem');
	}

	/** The last chip: "Se puede pedir que paren" / "Sin pedir que paren". */
	get stopChip(): Locator {
		return this.chips.nth(4);
	}

	/** The selected categories in game order (both the host's card and the guests' chips). */
	get categoryChips(): Locator {
		return this.settings.locator('ol > li');
	}

	/** Exactly these categories, in this order (each chip reads "<position> <label>"). */
	async expectCategories(labels: string[]): Promise<void> {
		await expect(this.categoryChips).toContainText(labels);
		await expect(this.categoryChips).toHaveCount(labels.length);
	}

	// ---- actions ----------------------------------------------------------------------------

	get startButton(): Locator {
		return this.button('lobby_start');
	}

	waitingForHost(hostName: string): Locator {
		return this.page.getByRole('status').filter({
			hasText: this.t('lobby_waiting_host', { name: hostName })
		});
	}

	async start(): Promise<void> {
		await expect(this.startButton).toBeEnabled();
		await this.startButton.click();
	}

	/** Leave -> confirmation sheet -> "Yes, leave". The app then goes Home. */
	async leave(): Promise<void> {
		await this.button('lobby_leave').click();
		await this.button('lobby_leave_confirm').click();
	}
}

/** The category sheet ("Edit categories"): selection, search, catalog and custom categories. */
export class CategoryPicker extends PageObject {
	get dialog(): Locator {
		return this.page.getByRole('dialog', { name: this.t('lobby_cats_title'), exact: true });
	}

	async open(): Promise<void> {
		await this.button('lobby_cats_edit').click();
		await expect(this.dialog).toBeVisible();
	}

	/** The catalog has been fetched: no skeleton rows are left. */
	async expectLoaded(): Promise<void> {
		await expect(this.dialog.getByText(this.t('lobby_cats_loading'), { exact: true })).toHaveCount(
			0
		);
		await expect(
			this.dialog.getByLabel(this.t('lobby_search_label'), { exact: true })
		).toBeVisible();
	}

	async done(): Promise<void> {
		await this.dialog.getByRole('button', { name: this.t('lobby_cats_done'), exact: true }).click();
		await expect(this.dialog).toBeHidden();
	}

	get selected(): Locator {
		return this.dialog
			.getByRole('region', { name: this.t('lobby_cats_selected_title'), exact: true })
			.getByRole('listitem');
	}

	async expectSelected(labels: string[]): Promise<void> {
		await expect(this.selected).toContainText(labels);
		await expect(
			this.dialog
				.getByRole('status')
				.filter({ hasText: this.t('lobby_cats_count', { count: labels.length, max: 10 }) })
		).toBeVisible();
	}

	async remove(label: string): Promise<void> {
		await this.dialog
			.getByRole('button', { name: this.t('lobby_cat_remove', { label }), exact: true })
			.click();
		await expect(this.selected.filter({ hasText: label })).toHaveCount(0);
	}

	/** Picks (or unpicks) a catalog category through the search box, which opens every matching group. */
	async toggle(label: string): Promise<void> {
		const search = this.dialog.getByLabel(this.t('lobby_search_label'), { exact: true });
		await search.fill(label);
		await this.dialog.getByRole('button', { name: label, exact: true }).first().click();
		await search.fill('');
	}

	async openCustom(): Promise<void> {
		await this.dialog
			.getByRole('button', { name: this.t('lobby_custom_add'), exact: true })
			.click();
	}

	get customInput(): Locator {
		return this.dialog.getByLabel(this.t('lobby_custom_label'), { exact: true });
	}

	/** The validation message under the custom field ("Ya existe: País"). */
	get customMessage(): Locator {
		return this.dialog.locator('#custom-message');
	}

	async typeCustom(label: string): Promise<void> {
		if (!(await this.customInput.isVisible())) await this.openCustom();
		await this.customInput.fill(label);
	}

	get customSubmit(): Locator {
		return this.dialog.getByRole('button', { name: this.t('lobby_custom_submit'), exact: true });
	}

	async addCustom(label: string): Promise<void> {
		await this.typeCustom(label);
		await expect(this.customSubmit).toBeEnabled();
		await this.customSubmit.click();
		await expect(this.selected.filter({ hasText: label })).toHaveCount(1);
	}
}
