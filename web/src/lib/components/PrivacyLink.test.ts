// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import PrivacyLink from './PrivacyLink.svelte';

describe('PrivacyLink', () => {
	beforeEach(() => i18n.set('es'));

	it.each([
		['es', 'Privacidad'],
		['en', 'Privacy policy'],
		['nl', 'Privacybeleid']
	] as const)(
		'is a plain link to the static policy in %s, opened as a full page load',
		(locale, text) => {
			i18n.set(locale);
			render(PrivacyLink);
			const link = screen.getByRole('link', { name: text });
			// `rel="external"`: SvelteKit must not treat it as an SPA route (the page is served by nginx)
			expect(link.getAttribute('rel')).toBe('external');
			expect(link.getAttribute('href')).toBe(`/privacy#${locale}`);
			// at least 48 px tall (touch target)
			expect(link.className).toContain('h-12');
		}
	);
});
