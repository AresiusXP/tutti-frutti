import { expect } from '@playwright/test';
import type { Locale } from './messages';
import type { Player } from './player';

export type NonSpanishLocale = Exclude<Locale, 'es'>;

/**
 * What may never show up on a screen of the language. English has no accented letter at all, so any
 * Spanish one is a leak there; Dutch spells "één", "Categorieën" and "kopiëren", so only the
 * characters Dutch does not have are banned.
 */
const SPANISH_ONLY: Record<NonSpanishLocale, RegExp> = {
	en: /[¡¿ñÑáéíóúüÁÉÍÓÚÜ]/u,
	nl: /[¡¿ñÑ]/u
};

/** The Spanish call, which must never show up on an English or a Dutch screen (both say "Stop!"). */
const SPANISH_WORDS = /\bbasta\b/i;

/**
 * Nothing Spanish on a screen of `locale` (`en` or `nl`): no `¡¿ñ` (and no Spanish accented vowel in
 * English), no "basta", neither in the visible text nor in the accessible names, and `<html lang>` is
 * the screen language. (Player names and answers are plain Latin text in these tests, so any hit is UI text.)
 */
export async function expectNoSpanish(
	players: Player[],
	locale: NonSpanishLocale,
	where: string
): Promise<void> {
	for (const player of players) {
		const text = await player.readableText();
		const offending = text
			.split('\n')
			.filter((line) => SPANISH_ONLY[locale].test(line) || SPANISH_WORDS.test(line));
		expect(offending, `Spanish text on ${player.name}'s ${where} screen (${locale})`).toEqual([]);
		await expect(player.page.locator('html')).toHaveAttribute('lang', locale, { timeout: 2000 });
	}
}

export const expectEnglishOnly = (players: Player[], where: string) =>
	expectNoSpanish(players, 'en', where);

export const expectDutchOnly = (players: Player[], where: string) =>
	expectNoSpanish(players, 'nl', where);
