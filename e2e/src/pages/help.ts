import type { Locator } from '@playwright/test';
import { PageObject } from './base';

/** The "How to play" guide (a sheet) and the one-time tip cards of the playing and review screens. */
export class HelpPage extends PageObject {
	/** The round "?" buttons (Home, lobby, review header) and the text links at the foot of the other screens. */
	get buttons(): Locator {
		return this.page.getByTestId('help-button');
	}

	get dialog(): Locator {
		return this.page.getByRole('dialog', { name: this.t('help_title'), exact: true });
	}

	/** The first help button of the screen. */
	async open(): Promise<void> {
		await this.buttons.first().click();
	}

	/** The part of the guide that is open (its header says it is expanded). */
	get openPart(): Locator {
		return this.dialog.locator('button[aria-expanded="true"]');
	}

	part(id: 'basics' | 'playing' | 'review' | 'points'): Locator {
		return this.dialog.locator(`#help-heading-${id}`);
	}

	/** Closes with the footer button ("Got it"); the sheet's ✕ has another name. */
	async close(): Promise<void> {
		await this.dialog.getByRole('button', { name: this.t('help_got_it'), exact: true }).click();
	}

	/** The one-time tip card on top of the playing and review screens. */
	get tip(): Locator {
		return this.page.getByRole('region', { name: this.t('help_tip_label'), exact: true });
	}

	async dismissTip(): Promise<void> {
		await this.tip.getByRole('button', { name: this.t('help_got_it'), exact: true }).click();
	}
}
