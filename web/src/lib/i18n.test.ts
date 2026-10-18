// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { i18n, LOCALE_LABELS } from './i18n.svelte';
import { m } from './paraglide/messages';

describe('i18n', () => {
	beforeEach(() => {
		i18n.set('es');
	});

	it('defaults to the base locale (es) and offers es + en + nl', () => {
		expect(i18n.locale).toBe('es');
		expect([...i18n.locales]).toEqual(['es', 'en', 'nl']);
		expect(LOCALE_LABELS).toEqual({ es: 'ES', en: 'EN', nl: 'NL' });
		expect(m.basta_button()).toBe('¡BASTA!');
	});

	it('switches language without a reload and persists the choice', () => {
		i18n.set('en');
		expect(i18n.locale).toBe('en');
		expect(m.basta_button()).toBe('Stop!');
		expect(localStorage.getItem('tf.locale')).toBe('en');
		expect(document.documentElement.lang).toBe('en');

		i18n.set('es');
		expect(m.basta_overlay()).toBe('¡Basta para mí, basta para todos!');
		expect(localStorage.getItem('tf.locale')).toBe('es');
	});

	it('switches to Dutch: only "Stop!", <html lang> follows', () => {
		i18n.set('nl');
		expect(i18n.locale).toBe('nl');
		expect(m.basta_button()).toBe('Stop!');
		expect(m.basta_overlay()).toBe('Stop!');
		expect(m.create_game()).toBe('Maak een spel');
		expect(localStorage.getItem('tf.locale')).toBe('nl');
		expect(document.documentElement.lang).toBe('nl');
	});

	it('ignores unsupported locales', () => {
		i18n.set('fr');
		expect(i18n.locale).toBe('es');
	});
});
