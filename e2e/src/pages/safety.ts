import { expect, type Locator, type Request, type Response } from '@playwright/test';
import type { ReportRequest } from '../../../web/src/lib/protocol/api';
import { PageObject } from './base';

/** The terms gate: the sheet shown before the first create / join of a guest that has not accepted. */
export class TermsGatePage extends PageObject {
	get dialog(): Locator {
		return this.page.getByRole('dialog', { name: this.t('terms_gate_title'), exact: true });
	}

	get checkbox(): Locator {
		return this.dialog.getByRole('checkbox', { name: this.t('terms_gate_age_label'), exact: true });
	}

	get acceptButton(): Locator {
		return this.dialog.getByRole('button', { name: this.t('terms_gate_accept'), exact: true });
	}

	get notNowButton(): Locator {
		return this.dialog.getByRole('button', { name: this.t('terms_gate_cancel'), exact: true });
	}

	get termsLink(): Locator {
		return this.dialog.getByRole('link', { name: this.t('terms_gate_read'), exact: true });
	}

	async expectOpen(): Promise<void> {
		await expect(this.dialog).toBeVisible();
		await expect(this.checkbox).toBeVisible();
	}

	async expectClosed(): Promise<void> {
		await expect(this.dialog).toHaveCount(0);
	}

	/** Ticks the box and presses Accept; resolves with the request the app sent to the server. */
	async accept(): Promise<
		ReportBody<{ version: string; locale: string; age13Confirmed: boolean }>
	> {
		await this.checkbox.check();
		const sent = this.page.waitForRequest(
			(request) => request.method() === 'POST' && /\/api\/terms\/accept$/.test(request.url())
		);
		const answered = this.page.waitForResponse((r) => /\/api\/terms\/accept$/.test(r.url()));
		await this.acceptButton.click();
		const [request, response] = await Promise.all([sent, answered]);
		expect(response.status()).toBe(200);
		return { body: request.postDataJSON() as never, request, response };
	}
}

export interface ReportBody<T> {
	body: T;
	request: Request;
	response: Response;
}

/** The report / block affordances of the room screens and their sheet. */
export class SafetyPage extends PageObject {
	/** The flag next to a player's row (lobby, scoreboard): "Reportar o bloquear a Ben". */
	flagPlayer(name: string): Locator {
		return this.page.getByRole('button', {
			name: this.t('report_player_aria', { name }),
			exact: true
		});
	}

	/** The flag of an answer card in the review: "Reportar o bloquear: respuesta de Ben". */
	flagAnswer(name: string): Locator {
		return this.page.getByRole('button', {
			name: this.t('report_answer_aria', { name }),
			exact: true
		});
	}

	get sheet(): Locator {
		return this.page.getByTestId('safety-sheet');
	}

	/** Any open dialog of the safety layer (menu, report form, block confirmation). */
	dialog(title: string): Locator {
		return this.page.getByRole('dialog', { name: title, exact: true });
	}

	menuItem(key: 'report_menu_player' | 'report_menu_answer' | 'report_menu_block'): Locator {
		return this.sheet.getByRole('button', { name: this.t(key), exact: true });
	}

	reason(reason: string): Locator {
		return this.page.getByRole('radio', { name: this.t(`report_reason_${reason}`), exact: true });
	}

	get comment(): Locator {
		return this.page.getByLabel(this.t('report_comment_label'), { exact: true });
	}

	get sendButton(): Locator {
		return this.page.getByRole('button', { name: this.t('report_send'), exact: true });
	}

	get retryButton(): Locator {
		return this.page.getByRole('button', { name: this.t('report_retry'), exact: true });
	}

	get alert(): Locator {
		return this.sheet.getByRole('alert');
	}

	/** The shield button of Home: terms, safety and legal. */
	get legalButton(): Locator {
		return this.page.getByRole('button', { name: this.t('legal_sheet_aria'), exact: true });
	}

	get legalSheet(): Locator {
		return this.page.getByTestId('legal-sheet');
	}

	get blockedSheet(): Locator {
		return this.page.getByTestId('blocked-sheet');
	}

	/** Home -> shield -> "Blocked players". */
	async openBlockedList(): Promise<void> {
		await this.legalButton.click();
		await this.legalSheet
			.getByRole('button', { name: this.t('blocked_link'), exact: true })
			.click();
		await expect(this.blockedSheet).toBeVisible();
	}

	unblockButton(name: string): Locator {
		return this.blockedSheet.getByRole('button', {
			name: this.t('blocked_unblock_aria', { name }),
			exact: true
		});
	}

	get sentToast(): Locator {
		return this.page.getByText(this.t('report_sent'), { exact: true });
	}

	/** Presses `button` and returns what went to `POST /api/reports` and how the server answered. */
	async submit(button: Locator): Promise<ReportBody<ReportRequest & { id?: string }>> {
		const sent = this.page.waitForRequest(
			(request) => request.method() === 'POST' && /\/api\/reports$/.test(request.url())
		);
		const answered = this.page.waitForResponse(
			(r) => r.request().method() === 'POST' && /\/api\/reports$/.test(r.url())
		);
		await button.click();
		const [request, response] = await Promise.all([sent, answered]);
		return { body: request.postDataJSON() as never, request, response };
	}
}
