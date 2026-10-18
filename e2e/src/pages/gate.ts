import { expect, type Locator } from '@playwright/test';
import { PageObject } from './base';

/** The invite gate (`/KT7P` in a tab that did not choose the room yet). */
export class GatePage extends PageObject {
	get nickname(): Locator {
		return this.page.getByLabel(this.t('gate_nickname_label'), { exact: true });
	}

	get joinButton(): Locator {
		return this.button('gate_join');
	}

	get homeLink(): Locator {
		return this.page.getByRole('link', { name: this.t('net_home'), exact: true });
	}

	/** The gate of an existing room: its code, the head count and the form. */
	async expectOpen(code: string): Promise<void> {
		await expect(
			this.page.getByRole('main', { name: this.t('gate_title'), exact: true })
		).toBeVisible();
		await expect(
			this.page.getByLabel(this.t('gate_code_aria', { code }), { exact: true })
		).toHaveText(code);
		await expect(this.nickname).toBeVisible();
	}

	async expectPlayers(count: number, max = 10): Promise<void> {
		await expect(
			this.page.getByRole('status').filter({ hasText: this.t('gate_players', { count, max }) })
		).toBeVisible();
	}

	async setNickname(name: string): Promise<void> {
		await this.nickname.fill(name);
		await expect(this.nickname).toHaveValue(name);
	}

	async join(): Promise<void> {
		await expect(this.joinButton).toBeEnabled();
		await this.joinButton.click();
	}

	/** One of the blocked states: `not_found | full | over | unavailable` (heading + explanation). */
	async expectBlocked(kind: 'not_found' | 'full' | 'over' | 'unavailable'): Promise<void> {
		await expect(
			this.page.getByRole('heading', { name: this.t(`gate_${kind}_title`), exact: true })
		).toBeVisible();
		await expect(
			this.page.getByText(this.t(`gate_${kind}_text`, kind === 'full' ? { max: 10 } : {}), {
				exact: true
			})
		).toBeVisible();
	}
}
