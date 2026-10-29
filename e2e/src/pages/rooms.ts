import { expect, type Locator } from '@playwright/test';
import { PageObject } from './base';

export class RoomsPage extends PageObject {
	get main(): Locator {
		return this.page.getByRole('main', { name: this.t('rooms_title'), exact: true });
	}

	get casualCaption(): Locator {
		return this.page.getByText(this.t('rooms_casual'), { exact: true });
	}

	joinButton(code: string): Locator {
		return this.page.getByRole('button', { name: this.t('rooms_join', { code }), exact: true });
	}

	card(code: string): Locator {
		return this.main.getByRole('listitem').filter({ has: this.joinButton(code) });
	}

	async open(): Promise<void> {
		await this.page.goto('/rooms');
		await expect(this.main).toBeVisible();
		await expect(this.casualCaption).toBeVisible();
	}
}
