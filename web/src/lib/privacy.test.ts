import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';
import { locales } from './paraglide/runtime';

const root = fileURLToPath(new URL('../../', import.meta.url));
const html = readFileSync(join(root, 'static/privacy.html'), 'utf8');
const css = readFileSync(join(root, 'static/privacy.css'), 'utf8');
const sections = html.split('<section class="doc"').slice(1);

/**
 * static/privacy.html is the policy linked from the Google Cloud consent screen. It is plain HTML
 * (no script), so these tests guard what the page says about the data the app really handles.
 */
describe('static/privacy.html', () => {
	it('has one section per app language, each linked from the language bar', () => {
		expect(sections).toHaveLength(locales.length);
		for (const locale of locales) {
			expect(html).toContain(`<section class="doc" id="${locale}" lang="${locale}">`);
			expect(html).toContain(`<a href="#${locale}" hreflang="${locale}" lang="${locale}">`);
			// the stylesheet switches language with :target
			expect(css).toContain(`#${locale}`);
		}
	});

	it('has the same numbered sections in every language', () => {
		const numbers = sections.map((s) => [...s.matchAll(/<h2>\s*(\d+)\./g)].map((m) => m[1]));
		expect(numbers[0]).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9', '10']);
		for (const n of numbers) expect(n).toEqual(numbers[0]);
	});

	it('states the same facts in every language: contact, cookies, scopes, retention, deletion', () => {
		for (const section of sections) {
			expect(section).toContain('mailto:aresiusxp@gmail.com');
			for (const cookie of ['tf_guest', 'tf_session', 'tf_oauth'])
				expect(section).toContain(cookie);
			expect(section).toContain('<code>openid</code>');
			expect(section).toContain('<code>profile</code>');
			expect(section).toContain(
				'https://developers.google.com/terms/api-services-user-data-policy'
			);
			expect(section).toContain('https://policies.google.com/privacy');
			// the optional AI judge sends the answers of a category to Cloudflare Workers AI
			expect(section).toContain('https://www.cloudflare.com/privacypolicy/');
			expect(section).toContain('Cloudflare');
			expect(section).toContain('24');
			expect(section).toContain('https://myaccount.google.com/permissions');
			// 30 days (session cookie and the answer time), 10 minutes (OAuth), 6 hours (cleanup), 7 days (logs)
			for (const figure of ['30', '10', '6', '7'])
				expect(section).toMatch(new RegExp(`\\b${figure}\\b`));
		}
	});

	it('explains public discovery and casual statistics in every language', () => {
		for (const [locale, discovery, privateRooms, casual] of [
			[
				'en',
				'Public room discovery',
				'Private rooms are not listed',
				'lifetime statistics are not recorded'
			],
			[
				'es',
				'buscador de salas públicas',
				'Las salas privadas no aparecen',
				'no se guardan estadísticas'
			],
			[
				'nl',
				'lijst met openbare kamers',
				'Privékamers staan niet in deze lijst',
				'levenslange statistieken worden niet bijgehouden'
			]
		]) {
			const section = sections.find((s) => s.includes(`id="${locale}"`))!;
			for (const fact of [discovery, privateRooms, casual]) expect(section).toContain(fact);
		}
	});

	it('writes the brand exactly like the constant, and names the app in the title', () => {
		for (const [found] of html.matchAll(/tutti\s+frutti/gi)) expect(found).toBe(BRAND);
		expect(html).toMatch(new RegExp(`<title>[^<]*${BRAND}</title>`));
	});

	it('never claims what the app does not do: no email, no picture, no analytics scripts', () => {
		// the Google scopes the server asks for (server/internal/auth/google.go): openid + profile only
		expect(html).not.toMatch(/\bemail\b[^<]*scope|scope[^<]*\bemail\b/i);
		expect(html).not.toMatch(/googletagmanager|google-analytics|gtag\(|fbq\(|hotjar/i);
	});
});
