// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const KEY = 'tf.locale';

function setBrowserLanguages(...languages: string[]) {
	vi.spyOn(navigator, 'languages', 'get').mockReturnValue(languages);
	vi.spyOn(navigator, 'language', 'get').mockReturnValue(languages[0]);
}

/** Loads the i18n modules from scratch, i.e. what a fresh page load does. */
async function freshPageLoad() {
	vi.resetModules();
	const runtime = await import('./paraglide/runtime');
	const { i18n } = await import('./i18n.svelte');
	const { m } = await import('./paraglide/messages');
	return { runtime, i18n, m };
}

describe('initial locale (strategy: localStorage -> preferredLanguage -> baseLocale)', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('follows an English browser on a fresh profile', async () => {
		setBrowserLanguages('en-US', 'en');
		const { i18n, m } = await freshPageLoad();
		expect(i18n.locale).toBe('en');
		expect(m.basta_button()).toBe('Stop!');
		expect(m.create_game()).toBe('Create game');
	});

	it('follows a Spanish browser (any region) on a fresh profile', async () => {
		for (const lang of ['es-AR', 'es-ES', 'es']) {
			setBrowserLanguages(lang);
			const { i18n, m } = await freshPageLoad();
			expect(i18n.locale, lang).toBe('es');
			expect(m.basta_button()).toBe('¡BASTA!');
		}
	});

	it('uses the first supported language of the browser preference list', async () => {
		setBrowserLanguages('fr-FR', 'en-GB', 'es');
		const { i18n } = await freshPageLoad();
		expect(i18n.locale).toBe('en');
	});

	it('falls back to Spanish when the browser language is not supported', async () => {
		setBrowserLanguages('fr-FR', 'de');
		const { i18n } = await freshPageLoad();
		expect(i18n.locale).toBe('es');
	});

	it('a stored choice beats the browser language', async () => {
		setBrowserLanguages('es-AR');
		localStorage.setItem(KEY, 'en');
		expect((await freshPageLoad()).i18n.locale).toBe('en');

		setBrowserLanguages('en-US');
		localStorage.setItem(KEY, 'es');
		expect((await freshPageLoad()).i18n.locale).toBe('es');
	});

	it('ignores an unsupported stored value and follows the browser language', async () => {
		// (Paraglide matches case-insensitively, so "EN" would count as "en".)
		for (const garbage of ['garbage', 'fr', 'english', 'en-US ', '', 'null', '{"locale":"en"}']) {
			// Each case starts from a clean profile: the first resolution persists its result.
			for (const [browser, expected] of [
				['en-US', 'en'],
				['fr-FR', 'es']
			] as const) {
				localStorage.clear();
				setBrowserLanguages(browser);
				localStorage.setItem(KEY, garbage);
				const { i18n } = await freshPageLoad();
				expect(i18n.locale, `${browser} browser + ${JSON.stringify(garbage)}`).toBe(expected);
			}
		}
	});

	it('remembers the detected language after the first visit (Paraglide persists it)', async () => {
		setBrowserLanguages('en-US');
		expect((await freshPageLoad()).i18n.locale).toBe('en');
		expect(localStorage.getItem(KEY)).toBe('en');

		// A later visit keeps it, even if the browser language has changed since.
		setBrowserLanguages('es-AR');
		expect((await freshPageLoad()).i18n.locale).toBe('en');
	});

	it('works when localStorage is unavailable', async () => {
		setBrowserLanguages('en-US');
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
			throw new DOMException('denied', 'SecurityError');
		});
		expect((await freshPageLoad()).i18n.locale).toBe('en');
	});

	it('is compiled with exactly the localStorage -> preferredLanguage -> baseLocale strategy', async () => {
		const { runtime } = await freshPageLoad();
		expect([...runtime.strategy]).toEqual(['localStorage', 'preferredLanguage', 'baseLocale']);
		expect(runtime.baseLocale).toBe('es');
	});
});
