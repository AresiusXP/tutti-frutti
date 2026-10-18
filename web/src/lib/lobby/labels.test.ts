import { existsSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type { Lang, Listing } from '$lib/protocol';
import {
	builtinIndex,
	checkCustom,
	cleanLabel,
	MAX_CATEGORIES,
	MAX_CUSTOM_LEN,
	MIN_CATEGORIES,
	normalizeLabel,
	runeCount,
	searchKey
} from './labels';
import { listingFor } from './fixtures';

describe('cleanLabel', () => {
	it('strips invisible characters, trims and collapses whitespace', () => {
		expect(cleanLabel('  \u200bMa\u00adrte \t del\n sur ')).toBe('Marte del sur');
		expect(cleanLabel('\u200b\u200b')).toBe('');
		expect(cleanLabel('   ')).toBe('');
		expect(cleanLabel('Ñandú')).toBe('Ñandú');
	});

	it('counts characters like the server (code points)', () => {
		expect(runeCount('Ñandú')).toBe(5);
		expect(runeCount('🦊🦊')).toBe(2);
		expect(runeCount('')).toBe(0);
	});
});

describe('normalizeLabel', () => {
	it('lowercases and strips diacritics', () => {
		expect(normalizeLabel('México', 'es')).toBe('mexico');
		expect(normalizeLabel('  Árbol  frondoso ', 'en')).toBe('arbol frondoso');
		expect(normalizeLabel('Straße Æon Œuvre', 'en')).toBe('strasse aeon oeuvre');
	});

	it('keeps ñ in Spanish lobbies and folds it in English ones', () => {
		expect(normalizeLabel('Ñandú', 'es')).toBe('ñandu');
		expect(normalizeLabel('Ñandú', 'en')).toBe('nandu');
		expect(normalizeLabel('n\u0303u', 'es')).toBe('ñu');
		expect(normalizeLabel('n \u0303', 'es')).toBe('n');
	});

	it('spells the ligature ĳ as ij in every language', () => {
		for (const lang of ['es', 'en', 'nl'] as const) {
			expect(normalizeLabel('Ĳsland', lang), lang).toBe('ijsland');
			expect(normalizeLabel('ĳsvogel', lang), lang).toBe('ijsvogel');
			expect(normalizeLabel('Bĳvoeglĳk naamwoord', lang), lang).toBe('bijvoeglijk naamwoord');
			expect(normalizeLabel('ĲSSEL', lang), lang).toBe('ijssel');
			expect(normalizeLabel('IJsland', lang), lang).toBe(normalizeLabel('Ĳsland', lang));
		}
	});

	it('keeps ñ only in Spanish lobbies, never in Dutch ones', () => {
		expect(normalizeLabel('Año', 'nl')).toBe('ano');
		expect(normalizeLabel('Año', 'es')).toBe('año');
	});

	it('is idempotent', () => {
		for (const text of [
			'México',
			'Ñandú',
			'Øl Łódź',
			'\u200bMa\u00adrte \u200b x',
			'Ĳsland Bĳ',
			'Zoë’s Ĳs'
		]) {
			for (const lang of ['es', 'en', 'nl'] as const) {
				const once = normalizeLabel(text, lang);
				expect(normalizeLabel(once, lang)).toBe(once);
			}
		}
	});
});

describe('searchKey', () => {
	it('ignores case and accents (ñ matches n)', () => {
		expect(searchKey('  ÑANDÚ  Río ')).toBe('nandu rio');
		expect(searchKey('Łódź')).toBe('lodz');
	});

	it('finds IJ words by typing ij or the ligature', () => {
		expect(searchKey('Ĳsland')).toBe('ijsland');
		expect(searchKey('ĲS')).toBe('ijs');
		expect(searchKey('Bĳvoeglĳk')).toBe('bijvoeglijk');
		expect(searchKey('Bijvoeglijk').includes(searchKey('ĳ'))).toBe(true);
	});
});

const goTest = new URL('../../../../server/internal/game/normalize_test.go', import.meta.url);

/** Drift guard: run the Go engine's own `TestNormalize` table through the TypeScript port. */
describe.skipIf(!existsSync(goTest))('normalizeLabel vs server normalize_test.go', () => {
	const source = existsSync(goTest) ? readFileSync(goTest, 'utf8') : '';
	const table = /func TestNormalize\(t \*testing\.T\) \{[\s\S]*?\n\t\}\n/.exec(source)?.[0] ?? '';
	const goString = (literal: string) =>
		JSON.parse(
			`"${literal.replace(/\\U([0-9a-fA-F]{8})/g, (_, hex) => String.fromCodePoint(parseInt(hex, 16)))}"`
		) as string;
	const rows = [
		...table.matchAll(
			/^\t\t\{"[^"]*",\s*"((?:[^"\\]|\\.)*)",\s*(?:Lang(ES|EN|NL)|"(\w+)"),\s*"((?:[^"\\]|\\.)*)"\},$/gm
		)
	].map((match) => ({
		input: goString(match[1]),
		lang: (match[2] ? match[2].toLowerCase() : match[3]) as Lang,
		want: goString(match[4])
	}));

	it('found the table', () => {
		expect(rows.length).toBeGreaterThan(40);
	});

	it('gives the same result for every row', () => {
		for (const row of rows) {
			expect(normalizeLabel(row.input, row.lang), JSON.stringify(row)).toBe(row.want);
		}
	});
});

describe('limits mirror the server', () => {
	const goSettings = new URL('../../../../server/internal/game/lang.go', import.meta.url);
	it.skipIf(!existsSync(goSettings))('uses the same constants as lang.go', () => {
		const source = readFileSync(goSettings, 'utf8');
		const value = (name: string) => Number(new RegExp(`${name}\\s*=\\s*(\\d+)`).exec(source)?.[1]);
		expect(MAX_CUSTOM_LEN).toBe(value('MaxCustomLen'));
		expect(MIN_CATEGORIES).toBe(value('MinCategories'));
		expect(MAX_CATEGORIES).toBe(value('MaxCategories'));
	});
});

describe('checkCustom', () => {
	const es = listingFor('es');
	const en = listingFor('en');
	const nl = listingFor('nl');
	const both = builtinIndex({ es, en }, 'es');
	const all = builtinIndex({ es, en, nl }, 'es');
	const onlyEs = builtinIndex({ es }, 'es');

	it('accepts a new label and returns it cleaned', () => {
		expect(checkCustom('  Algo   que  da risa ', 'es', both, [])).toEqual({
			ok: true,
			label: 'Algo que da risa'
		});
	});

	it('rejects empty and blank text', () => {
		expect(checkCustom('', 'es', both, [])).toEqual({ ok: false, error: 'empty' });
		expect(checkCustom(' \u200b  ', 'es', both, [])).toEqual({ ok: false, error: 'empty' });
	});

	it('rejects more than 24 characters but accepts exactly 24', () => {
		expect(checkCustom('a'.repeat(24), 'es', both, []).ok).toBe(true);
		expect(checkCustom('a'.repeat(25), 'es', both, [])).toEqual({
			ok: false,
			error: 'too_long',
			max: 24
		});
		expect(checkCustom('🦊'.repeat(24), 'es', both, []).ok).toBe(true);
	});

	it('rejects a built-in label ignoring case, accents and invisible characters', () => {
		expect(checkCustom('pais', 'es', both, [])).toEqual({
			ok: false,
			error: 'duplicate',
			existing: 'País'
		});
		expect(checkCustom('FRUTA\u200b  o  VERDURA', 'es', both, [])).toMatchObject({
			error: 'duplicate',
			existing: 'Fruta o verdura'
		});
	});

	it('rejects the built-in label of the OTHER language, shown in the lobby language', () => {
		expect(checkCustom('Country', 'es', both, [])).toEqual({
			ok: false,
			error: 'duplicate',
			existing: 'País'
		});
		const enIndex = builtinIndex({ es, en }, 'en');
		expect(checkCustom('pais', 'en', enIndex, [])).toEqual({
			ok: false,
			error: 'duplicate',
			existing: 'Country'
		});
		// without the other language loaded only the shown one is known
		expect(checkCustom('Country', 'es', onlyEs, []).ok).toBe(true);
	});

	it('rejects the built-in label of every language, shown in the lobby language', () => {
		const esIndex = builtinIndex({ es, en, nl }, 'es');
		const enIndex = builtinIndex({ es, en, nl }, 'en');
		const nlIndex = builtinIndex({ es, en, nl }, 'nl');
		const dup = (text: string, lang: Lang, index: typeof esIndex) => {
			const check = checkCustom(text, lang, index, []);
			return check.ok ? null : check.error === 'duplicate' ? check.existing : check.error;
		};
		// every spelling of one category is a duplicate in every lobby language
		for (const spelling of [
			'Adjetivo',
			'Adjective',
			'Bijvoeglijk naamwoord',
			'bijvoeglijk NAAMWOORD'
		]) {
			expect(dup(spelling, 'es', esIndex), spelling).toBe('Adjetivo');
			expect(dup(spelling, 'en', enIndex), spelling).toBe('Adjective');
			expect(dup(spelling, 'nl', nlIndex), spelling).toBe('Bijvoeglijk naamwoord');
		}
		// without the Dutch catalog loaded the Dutch label is still unknown
		expect(checkCustom('Naam', 'es', both, []).ok).toBe(true);
		expect(dup('Naam', 'es', esIndex)).toBe('Nombre');
	});

	it('knows the IJ ligature spelling of a Dutch built-in label', () => {
		expect(checkCustom('Bĳvoeglĳk naamwoord', 'nl', builtinIndex({ nl }, 'nl'), [])).toEqual({
			ok: false,
			error: 'duplicate',
			existing: 'Bijvoeglijk naamwoord'
		});
		expect(all.has('bijvoeglijk naamwoord')).toBe(true);
	});

	it('rejects a duplicate of another custom and reports that one', () => {
		expect(checkCustom('  algo QUE da risa', 'es', both, ['Algo que da risa'])).toEqual({
			ok: false,
			error: 'duplicate',
			existing: 'Algo que da risa'
		});
		expect(checkCustom('Otra cosa', 'es', both, ['Algo que da risa']).ok).toBe(true);
	});

	it('tells ñ from n in Spanish lobbies only', () => {
		expect(checkCustom('Año', 'es', both, ['Ano']).ok).toBe(true);
		expect(checkCustom('Año', 'en', both, ['Ano']).ok).toBe(false);
	});
});

describe('builtinIndex', () => {
	it('works with no listing at all', () => {
		expect(builtinIndex({}, 'es').size).toBe(0);
	});

	it('falls back to the own label when the shown language lacks the id', () => {
		const partial: Listing = {
			lang: 'en',
			groups: ['x'],
			categories: [{ id: 'zzz', group: 'x', label: 'Zed', default: false }]
		};
		expect(builtinIndex({ en: partial }, 'es').get('zed')).toBe('Zed');
	});
});
