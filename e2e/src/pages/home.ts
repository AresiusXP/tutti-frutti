import { expect, type Locator } from '@playwright/test';
import type { Locale } from '../messages';
import { PageObject } from './base';

export const ROOM_CODE = /^[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/;
export const ROOM_URL = /\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/;

export interface AvatarChoice {
	emoji: string;
	/** `banana | orange | strawberry | lime | grape | blueberry` */
	color: string;
}

export class HomePage extends PageObject {
	get nickname(): Locator {
		return this.page.getByLabel(this.t('nickname_label'), { exact: true });
	}

	get createButton(): Locator {
		return this.button('create_game');
	}

	async setVisibility(visibility: 'private' | 'public'): Promise<void> {
		const choice = this.page
			.getByRole('group', { name: this.t('rooms_visibility'), exact: true })
			.getByRole('button', { name: this.t(`rooms_${visibility}`), exact: true });
		await choice.click();
		await expect(choice).toHaveAttribute('aria-pressed', 'true');
	}

	get joinButton(): Locator {
		return this.button('join_game');
	}

	/** The quiet link to the static privacy policy at the bottom (`/privacy#<language>`). */
	get privacyLink(): Locator {
		return this.page.getByRole('link', { name: this.t('privacy_link'), exact: true });
	}

	/** The "support the game" link next to it: Cafecito in Argentina, Ko-fi elsewhere, a new tab either way. */
	get supportLink(): Locator {
		return this.page.getByRole('link', { name: this.t('support_link') });
	}

	codeBox(index: number): Locator {
		return this.page.getByRole('textbox', {
			name: this.t('code_char_label', { n: index + 1, total: 4 }),
			exact: true
		});
	}

	get emojiGroup(): Locator {
		return this.page.getByRole('group', { name: this.t('avatar_emoji_group'), exact: true });
	}

	get colorGroup(): Locator {
		return this.page.getByRole('group', { name: this.t('avatar_color_group'), exact: true });
	}

	/** A toast (they live in a polite live region at the bottom). */
	toast(text: string): Locator {
		return this.page.getByText(text, { exact: true });
	}

	async open(): Promise<void> {
		await this.page.goto('/');
		await expect(this.nickname).toBeVisible();
	}

	async setNickname(name: string): Promise<void> {
		await this.nickname.fill(name);
		await expect(this.nickname).toHaveValue(name);
	}

	async pickAvatar({ emoji, color }: AvatarChoice): Promise<void> {
		await this.emojiGroup.getByRole('button', { name: emoji, exact: true }).click();
		await this.colorGroup
			.getByRole('button', { name: this.t(`color_${color}`), exact: true })
			.click();
		await this.expectAvatar({ emoji, color });
	}

	async expectAvatar({ emoji, color }: AvatarChoice): Promise<void> {
		await expect(this.emojiGroup.getByRole('button', { name: emoji, exact: true })).toHaveAttribute(
			'aria-pressed',
			'true'
		);
		await expect(
			this.colorGroup.getByRole('button', { name: this.t(`color_${color}`), exact: true })
		).toHaveAttribute('aria-pressed', 'true');
	}

	/** Types the code into the four boxes, one character per box. */
	async enterCode(code: string): Promise<void> {
		for (const [i, char] of Array.from(code).entries()) {
			await this.codeBox(i).fill(char);
		}
		for (const [i, char] of Array.from(code.toUpperCase()).entries()) {
			await expect(this.codeBox(i)).toHaveValue(char);
		}
	}

	/** Creates a room and waits until the app navigated to it. Returns the room code. */
	async create(): Promise<string> {
		await this.createButton.click();
		await this.page.waitForURL(/\/[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$/);
		return new URL(this.page.url()).pathname.slice(1);
	}

	/** Types a code and presses "Join game" (does not wait for the outcome). */
	async submitCode(code: string): Promise<void> {
		await this.enterCode(code);
		await expect(this.joinButton).toBeEnabled();
		await this.joinButton.click();
	}

	/** Presses ES / EN / NL; from now on the player's labels are looked up in the new language. */
	async switchLanguage(locale: Locale): Promise<void> {
		const label = locale.toUpperCase();
		const current = this.page.getByRole('group', { name: this.t('language_label'), exact: true });
		await current.getByRole('button', { name: label, exact: true }).click();
		this.player.locale = locale;
		const group = this.page.getByRole('group', { name: this.t('language_label'), exact: true });
		await expect(group.getByRole('button', { name: label, exact: true })).toHaveAttribute(
			'aria-pressed',
			'true'
		);
		await expect(this.page.locator('html')).toHaveAttribute('lang', locale);
	}
}
