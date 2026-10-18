import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';
import { m } from './paraglide/messages';

type Catalog = Record<string, string>;

/** Message areas: the shared catalog plus one folder per feature (each owned by one part of the app). */
const AREAS = ['', 'net/', 'lobby/', 'play/', 'review/', 'account/'];

function readArea(area: string, locale: string): Catalog {
	const raw = JSON.parse(
		readFileSync(new URL(`../../messages/${area}${locale}.json`, import.meta.url), 'utf8')
	) as Catalog;
	return Object.fromEntries(Object.entries(raw).filter(([key]) => !key.startsWith('$')));
}

function load(locale: string): Catalog {
	const merged: Catalog = {};
	for (const area of AREAS) {
		for (const [key, value] of Object.entries(readArea(area, locale))) {
			if (key in merged)
				throw new Error(`duplicate message key "${key}" (${area || 'core'}/${locale})`);
			merged[key] = value;
		}
	}
	return merged;
}

const es = load('es');
const en = load('en');
const params = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort();

describe('message catalogs', () => {
	it('es and en define exactly the same keys', () => {
		expect(Object.keys(en).sort()).toEqual(Object.keys(es).sort());
	});

	it('uses the same {parameters} in both languages', () => {
		for (const key of Object.keys(es)) {
			expect(params(en[key]), key).toEqual(params(es[key]));
		}
	});

	it('never contains the brand as literal text', () => {
		for (const [locale, catalog] of Object.entries({ es, en })) {
			for (const [key, value] of Object.entries(catalog)) {
				expect(value.toLowerCase(), `${locale}.${key}`).not.toContain(BRAND.toLowerCase());
				expect(value.toLowerCase().replace(/\s/g, ''), `${locale}.${key}`).not.toContain(
					BRAND.toLowerCase().replace(/\s/g, '')
				);
			}
		}
	});

	it('passes the brand via {brand} where a message needs it', () => {
		expect(params(es.share_text)).toEqual(['brand', 'code']);
		expect(params(en.share_text)).toEqual(['brand', 'code']);
		expect(m.share_text({ brand: BRAND, code: 'KT7P' }, { locale: 'es' })).toBe(
			'¡Jugá Tutti Frutti conmigo! Código KT7P'
		);
		expect(m.share_text({ brand: BRAND, code: 'KT7P' }, { locale: 'en' })).toBe(
			'Play Tutti Frutti with me! Code KT7P'
		);
	});

	it('keeps the BRAND constant and the static HTML shell in sync', () => {
		expect(BRAND).toBe('Tutti Frutti');
		const html = readFileSync(new URL('../app.html', import.meta.url), 'utf8');
		expect(html).toContain(`<title>${BRAND}</title>`);
	});
});

describe('Basta / Stop wording', () => {
	it('English says exactly "Stop!" with no Spanish text', () => {
		expect(en.basta_button).toBe('Stop!');
		expect(en.basta_overlay).toBe('Stop!');
		expect(en.letter_stop).toBe('Stop!');
		expect(m.basta_button({}, { locale: 'en' })).toBe('Stop!');
		expect(m.basta_overlay({}, { locale: 'en' })).toBe('Stop!');
		expect(m.letter_stop({}, { locale: 'en' })).toBe('Stop!');
	});

	it('Spanish uses ¡BASTA! and the full overlay sentence', () => {
		expect(es.basta_button).toBe('¡BASTA!');
		expect(es.basta_overlay).toBe('¡Basta para mí, basta para todos!');
		expect(es.letter_stop).toBe('¡STOP!');
		expect(m.basta_button({}, { locale: 'es' })).toBe('¡BASTA!');
	});

	it('English strings contain no Spanish punctuation or accents', () => {
		for (const [key, value] of Object.entries(en)) {
			expect(value, key).not.toMatch(/[¡¿ñáéíóúü]/i);
		}
	});

	it('Spanish uses voseo', () => {
		expect(es.create_game).toMatch(/^Creá/);
		expect(es.join_game).toMatch(/^Unite/);
	});
});
