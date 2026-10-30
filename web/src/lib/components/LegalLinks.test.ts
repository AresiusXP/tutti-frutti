// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import LegalLinks from './LegalLinks.svelte';

describe('LegalLinks', () => {
	beforeEach(() => i18n.set('es'));

	it.each([
		['es', 'Privacidad', 'Términos', 'Eliminar cuenta'],
		['en', 'Privacy policy', 'Terms', 'Delete account'],
		['nl', 'Privacybeleid', 'Voorwaarden', 'Account verwijderen']
	] as const)(
		'links the static pages in %s as full page loads in the same browsing context',
		(locale, privacy, terms, remove) => {
			i18n.set(locale);
			render(LegalLinks, { pages: ['privacy', 'terms', 'delete-account'] });
			for (const [name, path] of [
				[privacy, 'privacy'],
				[terms, 'terms'],
				[remove, 'delete-account']
			]) {
				const link = screen.getByRole('link', { name });
				// `rel="external"`: SvelteKit must not treat it as an SPA route (nginx serves the page)
				expect(link.getAttribute('rel')).toBe('external');
				expect(link.getAttribute('href')).toBe(`/${path}#${locale}`);
				// the Android app comes back with Back: never a new tab or window
				expect(link.getAttribute('target')).toBeNull();
				expect(link.className).toContain('h-12');
			}
		}
	);

	it('defaults to the privacy policy and the terms, in a named navigation landmark', () => {
		i18n.set('en');
		render(LegalLinks);
		expect(screen.getAllByRole('link').map((a) => a.textContent?.trim())).toEqual([
			'Privacy policy',
			'Terms'
		]);
		expect(screen.getByRole('navigation', { name: 'Legal information' })).toBeTruthy();
	});
});
