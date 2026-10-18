import type { Locator, Page } from '@playwright/test';
import type { Params } from '../messages';
import type { Player } from '../player';

/** Base of every page object: one per screen of the app, bound to one Player (browser context). */
export abstract class PageObject {
	constructor(protected readonly player: Player) {}

	protected get page(): Page {
		return this.player.page;
	}

	protected t(key: string, params?: Params): string {
		return this.player.t(key, params);
	}

	/** Exact-name button of the player's UI language. */
	protected button(key: string, params?: Params): Locator {
		return this.page.getByRole('button', { name: this.t(key, params), exact: true });
	}
}
