import { expect, type Locator } from '@playwright/test';
import { PageObject } from './base';

export type ClosedReason = 'room_gone' | 'kicked' | 'left' | 'replaced' | 'join_rejected';

/** The screens of a session that ended for good (kicked, room gone, replaced by another tab, ...). */
export class ClosedPage extends PageObject {
	get homeLink(): Locator {
		return this.page.getByRole('link', { name: this.t('net_home'), exact: true });
	}

	get takeSeatBack(): Locator {
		return this.button('closed_replaced_action');
	}

	async expect(reason: ClosedReason, text?: string): Promise<void> {
		const title = reason === 'join_rejected' ? 'closed_rejected_title' : `closed_${reason}_title`;
		await expect(
			this.page.getByRole('heading', { name: this.t(title), exact: true })
		).toBeVisible();
		if (text) await expect(this.page.getByText(text, { exact: true })).toBeVisible();
		await expect(this.homeLink).toBeVisible();
	}
}
