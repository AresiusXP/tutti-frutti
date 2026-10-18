// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { i18n } from './i18n.svelte';
import { m } from './paraglide/messages';

describe('i18n', () => {
	beforeEach(() => {
		i18n.set('es');
	});

	it('defaults to the base locale (es) and offers es + en', () => {
		expect(i18n.locale).toBe('es');
		expect([...i18n.locales]).toEqual(['es', 'en']);
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

	it('ignores unsupported locales', () => {
		i18n.set('fr');
		expect(i18n.locale).toBe('es');
	});
});
