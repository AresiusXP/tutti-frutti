import { existsSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { ALPHABETS, clampRounds, HARD_LETTERS, LANGS, maxRounds, playableLetters } from './letters';

describe('letters', () => {
	it('has 27 Spanish letters (Ñ after N) and 26 English letters', () => {
		expect(ALPHABETS.es).toHaveLength(27);
		expect(ALPHABETS.es.indexOf('Ñ')).toBe(ALPHABETS.es.indexOf('N') + 1);
		expect(ALPHABETS.en).toHaveLength(26);
		expect(ALPHABETS.en).not.toContain('Ñ');
		expect(LANGS).toEqual(['es', 'en']);
	});

	it('lists the hard letters of the design (K Ñ Q W X Y Z / Q U V X Y Z)', () => {
		expect(HARD_LETTERS.es).toEqual(['K', 'Ñ', 'Q', 'W', 'X', 'Y', 'Z']);
		expect(HARD_LETTERS.en).toEqual(['Q', 'U', 'V', 'X', 'Y', 'Z']);
	});

	it('computes the playable letters per language and toggle', () => {
		expect(playableLetters('es', false)).toHaveLength(27);
		expect(playableLetters('es', true)).toHaveLength(20);
		expect(playableLetters('es', true)).not.toContain('Ñ');
		expect(playableLetters('en', false)).toHaveLength(26);
		expect(playableLetters('en', true)).toHaveLength(20);
		expect(playableLetters('en', true).slice(0, 3)).toEqual(['A', 'B', 'C']);
		expect(maxRounds('es', false)).toBe(27);
		expect(maxRounds('en', true)).toBe(20);
	});

	it('clamps rounds: 0 is unlimited, anything else is 1..max', () => {
		expect(clampRounds(0, 'es', true)).toBe(0);
		expect(clampRounds(-3, 'es', true)).toBe(0);
		expect(clampRounds(Number.NaN, 'es', true)).toBe(0);
		expect(clampRounds(5, 'es', true)).toBe(5);
		expect(clampRounds(0.4, 'es', true)).toBe(1);
		expect(clampRounds(1.9, 'es', true)).toBe(1);
		expect(clampRounds(27, 'es', true)).toBe(20);
		expect(clampRounds(27, 'es', false)).toBe(27);
		expect(clampRounds(99, 'en', false)).toBe(26);
	});
});

const goFile = new URL('../../../../server/internal/game/lang.go', import.meta.url);

/** Drift guard: the tables above must match the Go engine (skipped when the server source is absent, e.g. a web-only checkout). */
describe.skipIf(!existsSync(goFile))('letters vs server/internal/game/lang.go', () => {
	const source = existsSync(goFile) ? readFileSync(goFile, 'utf8') : '';

	function alphabet(lang: 'ES' | 'EN'): string[] {
		const block = /var alphabets = map\[Lang\]\[\]string\{([\s\S]*?)\n\}/.exec(source);
		expect(block, 'alphabets table not found').not.toBeNull();
		const line = new RegExp(`Lang${lang}:\\s*strings\\.Split\\("([^"]+)",\\s*" "\\)`).exec(
			block![1]
		);
		expect(line, `alphabet ${lang} not found`).not.toBeNull();
		return line![1].split(' ');
	}

	function hard(lang: 'ES' | 'EN'): string[] {
		const block = /var hardLetters = map\[Lang\]\[\]string\{([\s\S]*?)\n\}/.exec(source);
		expect(block, 'hardLetters table not found').not.toBeNull();
		const line = new RegExp(`Lang${lang}:\\s*\\{([^}]*)\\}`).exec(block![1]);
		expect(line, `hard letters ${lang} not found`).not.toBeNull();
		return [...line![1].matchAll(/"([^"]+)"/g)].map((match) => match[1]);
	}

	it('has the same alphabets', () => {
		expect([...ALPHABETS.es]).toEqual(alphabet('ES'));
		expect([...ALPHABETS.en]).toEqual(alphabet('EN'));
	});

	it('has the same hard letters', () => {
		expect([...HARD_LETTERS.es]).toEqual(hard('ES'));
		expect([...HARD_LETTERS.en]).toEqual(hard('EN'));
	});
});
