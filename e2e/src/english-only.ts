import { expect } from '@playwright/test';
import { SPANISH_CHARS } from './messages';
import type { Player } from './player';

/** The Spanish call, which must never show up on an English screen. */
const SPANISH_WORDS = /\bbasta\b/i;

/**
 * Nothing Spanish on an English screen: no `¡¿ñáéíóúü`, no "basta", neither in the visible text nor
 * in the accessible names. (Player names and answers are ASCII in the English tests, so any hit is UI text.)
 */
export async function expectEnglishOnly(players: Player[], where: string): Promise<void> {
	for (const player of players) {
		const text = await player.readableText();
		const offending = text
			.split('\n')
			.filter((line) => SPANISH_CHARS.test(line) || SPANISH_WORDS.test(line));
		expect(offending, `Spanish text on ${player.name}'s ${where} screen`).toEqual([]);
		await expect(player.page.locator('html')).toHaveAttribute('lang', 'en');
	}
}
