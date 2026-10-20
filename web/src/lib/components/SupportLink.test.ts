// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { SUPPORT_ARGENTINA, SUPPORT_DEFAULT } from '$lib/support/support';
import SupportLink from './SupportLink.svelte';

const device = (timeZone: string, languages: string[]) => {
	vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(
		() => ({ resolvedOptions: () => ({ timeZone }) }) as Intl.DateTimeFormat
	);
	vi.spyOn(navigator, 'languages', 'get').mockReturnValue(languages);
};

describe('SupportLink', () => {
	beforeEach(() => i18n.set('es'));
	afterEach(() => vi.restoreAllMocks());

	it.each([
		['es', 'Invitame un café'],
		['en', 'Support the game'],
		['nl', 'Steun het spel']
	] as const)('is a plain outbound link named in %s, never after the platform', (locale, text) => {
		device('America/Argentina/Buenos_Aires', ['es-AR']);
		i18n.set(locale);
		render(SupportLink);
		// the name starts with the visible words (WCAG 2.5.3) and says it opens a new tab
		const link = screen.getByRole('link', { name: new RegExp(`^${text}`) });
		expect(link.getAttribute('target')).toBe('_blank');
		expect(link.getAttribute('rel')).toBe('external noopener noreferrer');
		expect(link.className).toContain('h-12'); // at least 48 px tall (touch target)
		expect(link.textContent).not.toMatch(/cafecito|ko-fi/i);
	});

	it('goes to Cafecito on an Argentine device, whatever the screen language', () => {
		device('America/Argentina/Cordoba', ['en-US']);
		i18n.set('en');
		render(SupportLink);
		expect(screen.getByRole('link').getAttribute('href')).toBe(SUPPORT_ARGENTINA);
	});

	it('goes to Ko-fi everywhere else, whatever the screen language', () => {
		device('Europe/Amsterdam', ['nl-NL']);
		i18n.set('es');
		render(SupportLink);
		expect(screen.getByRole('link').getAttribute('href')).toBe(SUPPORT_DEFAULT);
	});

	it('follows the screen language when it changes', async () => {
		device('Europe/Amsterdam', ['nl-NL']);
		render(SupportLink);
		expect(screen.getByRole('link').textContent).toContain('Invitame un café');
		i18n.set('nl');
		await Promise.resolve();
		expect(screen.getByRole('link').textContent).toContain('Steun het spel');
	});

	it('puts the coffee cup out of reach of screen readers', () => {
		device('Europe/Amsterdam', ['en-US']);
		render(SupportLink);
		expect(screen.getByText('☕').getAttribute('aria-hidden')).toBe('true');
	});
});
