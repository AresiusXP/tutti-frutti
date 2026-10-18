import { describe, expect, it } from 'vitest';
import {
	MAX_ANSWER_LEN,
	answerToSend,
	fieldStatus,
	normalizeAnswer,
	startsWithLetter,
	stripFormat
} from './answer';

// Ported from server/internal/game/normalize_test.go: keep both in sync.
const normalizeCases: [name: string, input: string, lang: string, want: string][] = [
	['accent', 'México', 'es', 'mexico'],
	['already plain', 'mexico', 'es', 'mexico'],
	['accent en', 'México', 'en', 'mexico'],
	['trim and lower', '  Árbol  ', 'es', 'arbol'],
	['collapse spaces', '  la   casa\t\nblanca ', 'es', 'la casa blanca'],
	['diaeresis', 'pingüino', 'es', 'pinguino'],
	['upper diaeresis', 'ÜBER', 'en', 'uber'],
	['enye kept in es', 'Ñandú', 'es', 'ñandu'],
	['enye upper in es', 'ÑANDÚ', 'es', 'ñandu'],
	['enye folds in en', 'Ñandú', 'en', 'nandu'],
	['enye folds in unknown lang', 'Ñandú', 'fr', 'nandu'],
	['decomposed enye in es', 'n\u0303andu', 'es', 'ñandu'],
	['decomposed enye in en', 'N\u0303andu', 'en', 'nandu'],
	['tilde not after n is dropped', 'a\u0303', 'es', 'a'],
	['tilde after space is dropped', 'n \u0303', 'es', 'n'],
	['tilde at start is dropped', '\u0303n', 'es', 'n'],
	['decomposed accent', 'cafe\u0301', 'es', 'cafe'],
	['ligatures', 'Straße Æon Œuvre', 'en', 'strasse aeon oeuvre'],
	['latin extended', 'Łódź Çağrı Škoda', 'en', 'lodz cagri skoda'],
	['non-breaking space', 'a\u00a0b', 'en', 'a b'],
	['empty', '', 'es', ''],
	['blank', ' \t ', 'es', ''],
	['digits and punctuation kept', 'R2-D2!', 'en', 'r2-d2!'],
	['leading zero width space', '\u200bMéxico', 'es', 'mexico'],
	['zero width space inside', 'Mé\u200bxico', 'en', 'mexico'],
	['soft hyphen inside', 'Ma\u00adrte', 'es', 'marte'],
	['zero width non-joiner and joiner', 'a\u200cb\u200dc', 'en', 'abc'],
	['word joiner and BOM', '\u2060Perro\ufeff', 'es', 'perro'],
	['bidi marks', '\u200ePerro\u200f', 'en', 'perro'],
	[
		'format characters around spaces collapse like spaces',
		'\u200b  la \u200b\u200b casa \u200b',
		'es',
		'la casa'
	],
	['format characters do not create spaces', 'la\u200bcasa', 'es', 'lacasa'],
	['only format characters', '\u200b\u200c\u00ad\ufeff', 'es', ''],
	['format characters between n and the combining tilde', 'n\u200b\u0303u', 'es', 'ñu'],
	['stacked marks in one letter', 'Ắng', 'es', 'ang'],
	['stacked marks decomposed', 'A\u0306\u0301ng', 'es', 'ang'],
	['vietnamese', 'Việt Nam', 'en', 'viet nam'],
	['ring and caron', 'Åke Čapek', 'es', 'ake capek'],
	['stroked o', 'Ø', 'en', 'o'],
	['stroked o in a word', 'Øresund', 'es', 'oresund'],
	['stroked l', 'Ł', 'en', 'l'],
	['stroked l in a word', 'Łódź', 'es', 'lodz'],
	['stroked d', 'Đà Nẵng', 'en', 'da nang'],
	['eth and stroked d', 'Ðorđe', 'es', 'dorde'],
	['dotless i', 'Iıİ', 'en', 'iii'],
	['capital sharp s', 'STRAẞE', 'en', 'strasse'],
	['NFC enye in es', '\u00d1and\u00fa', 'es', 'ñandu'],
	['NFD enye in es', 'N\u0303and\u0075\u0301', 'es', 'ñandu'],
	['NFC enye in en', '\u00d1and\u00fa', 'en', 'nandu'],
	['NFD enye in en', 'N\u0303and\u0075\u0301', 'en', 'nandu'],
	['lower NFD enye in es', 'n\u0303u', 'es', 'ñu'],
	['enye after an accented n mark', 'n\u0301\u0303', 'es', 'ñ'],
	['space between n and the tilde', 'n \u0303', 'es', 'n']
];

describe('normalizeAnswer (mirror of game.Normalize)', () => {
	it.each(normalizeCases)('%s', (_name, input, lang, want) => {
		expect(normalizeAnswer(input, lang)).toBe(want);
	});

	it('NFC and NFD spellings agree', () => {
		const words: [string, string, string][] = [
			['México', 'mexico', 'mexico'],
			['árbol', 'arbol', 'arbol'],
			['Ñandú', 'ñandu', 'nandu'],
			['ÑANDÚ', 'ñandu', 'nandu'],
			['pingüino', 'pinguino', 'pinguino'],
			['Ắng  Việt', 'ang viet', 'ang viet'],
			['  Cañón del Colorado ', 'cañon del colorado', 'canon del colorado']
		];
		for (const [word, es, en] of words) {
			const nfc = word.normalize('NFC');
			const nfd = word.normalize('NFD');
			expect(nfc).not.toBe(nfd);
			expect(normalizeAnswer(nfc, 'es')).toBe(es);
			expect(normalizeAnswer(nfd, 'es')).toBe(es);
			expect(normalizeAnswer(nfc, 'en')).toBe(en);
			expect(normalizeAnswer(nfd, 'en')).toBe(en);
		}
	});

	it('is idempotent', () => {
		const samples = [
			'México',
			'Ñandú',
			'  Árbol  frondoso ',
			'Straße',
			'Ắng',
			'Øl Łódź',
			'N\u0303u',
			'\u200bMa\u00adrte \u200b x'
		];
		for (const s of samples) {
			for (const lang of ['es', 'en']) {
				const once = normalizeAnswer(s, lang);
				expect(normalizeAnswer(once, lang)).toBe(once);
			}
		}
	});

	it('lower-cases per character like Go (no final-sigma context rule)', () => {
		expect(normalizeAnswer('ΟΔΟΣ', 'en')).toBe('οδοσ');
	});
});

describe('stripFormat', () => {
	it('removes every Unicode format character and keeps ordinary text', () => {
		for (const r of ['\u00ad', '\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u2060']) {
			expect(stripFormat(`a${r}b`)).toBe('ab');
		}
		expect(stripFormat('a\u2066b\ufeffc\u{e0041}d')).toBe('abcd');
		expect(stripFormat('Ñandú 🦊 a-b')).toBe('Ñandú 🦊 a-b');
	});
});

const startsCases: [name: string, answer: string, letter: string, lang: string, want: boolean][] = [
	['plain', 'Argentina', 'A', 'es', true],
	['lowercase answer', 'argentina', 'A', 'es', true],
	['accent answer', 'Árbol', 'A', 'es', true],
	['accented letter input', 'Arbol', 'Á', 'es', true],
	['México for M', 'México', 'M', 'es', true],
	['leading spaces', '   ana', 'A', 'es', true],
	['wrong letter', 'Brasil', 'A', 'es', false],
	['enye is its own letter in es', 'Ñandú', 'N', 'es', false],
	['enye matches enye in es', 'Ñandú', 'Ñ', 'es', true],
	['n is not enye in es', 'Nube', 'Ñ', 'es', false],
	['enye folds to n in en', 'Ñandú', 'N', 'en', true],
	['umlaut', 'Ürsula', 'U', 'en', true],
	['empty answer', '', 'A', 'es', false],
	['blank answer', '   ', 'A', 'es', false],
	['leading punctuation is not skipped', '¿Ana?', 'A', 'es', false],
	['empty letter', 'Ana', '', 'es', false],
	['NFD accent on the first letter', 'A\u0301frica', 'A', 'es', true],
	['NFD accent on the first letter, accented letter', 'A\u0301frica', '\u00c1', 'en', true],
	['NFC accent on the first letter', '\u00c1frica', 'A', 'es', true],
	['NFD enye is its own letter in es', 'N\u0303u', '\u00d1', 'es', true],
	['NFD enye, NFD letter in es', 'N\u0303u', 'N\u0303', 'es', true],
	['NFD enye is not N in es', 'N\u0303u', 'N', 'es', false],
	['NFD enye folds to N in en', 'N\u0303u', 'N', 'en', true],
	['NFD enye matches the enye letter in en', 'N\u0303u', '\u00d1', 'en', true],
	['NFC enye is not N in es', '\u00d1u', 'N', 'es', false],
	['NFC enye folds to N in en', '\u00d1u', 'N', 'en', true],
	['stacked marks', '\u1eaeng', 'A', 'es', true],
	['stroked o', '\u00d8l', 'O', 'en', true],
	['stroked l', '\u0141\u00f3d\u017a', 'L', 'en', true],
	['leading zero width space', '\u200bPerro', 'P', 'es', true],
	['leading BOM and soft hyphen', '\ufeff\u00adPerro', 'P', 'en', true],
	['format character in the letter', 'Perro', '\u200bP', 'es', true],
	['only format characters is no answer', '\u200b', 'P', 'es', false],
	['format characters before an enye in es', '\u200bN\u0303u', '\u00d1', 'es', true],
	['a plain N is not an NFD enye letter in es', 'Nube', 'N\u0303', 'es', false]
];

describe('startsWithLetter (mirror of game.StartsWithLetter)', () => {
	it.each(startsCases)('%s', (_name, answer, letter, lang, want) => {
		expect(startsWithLetter(answer, letter, lang)).toBe(want);
	});
});

describe('fieldStatus', () => {
	it('tells empty, wrong letter and valid apart', () => {
		expect(fieldStatus('', 'F', 'es')).toBe('empty');
		expect(fieldStatus('  \u200b ', 'F', 'es')).toBe('empty');
		expect(fieldStatus('Brasil', 'F', 'es')).toBe('wrong_letter');
		expect(fieldStatus('Francia', 'F', 'es')).toBe('valid');
		expect(fieldStatus('  fresa', 'F', 'en')).toBe('valid');
	});

	it('treats ñ as its own letter only in Spanish lobbies', () => {
		expect(fieldStatus('Ñandú', 'N', 'es')).toBe('wrong_letter');
		expect(fieldStatus('Ñandú', 'Ñ', 'es')).toBe('valid');
		expect(fieldStatus('Ñandú', 'N', 'en')).toBe('valid');
		expect(fieldStatus('Nube', 'Ñ', 'es')).toBe('wrong_letter');
	});

	it('is accent-insensitive', () => {
		expect(fieldStatus('Árbol', 'A', 'es')).toBe('valid');
		expect(fieldStatus('África', 'a', 'en')).toBe('valid');
	});
});

describe('answerToSend', () => {
	it('trims', () => {
		expect(answerToSend('  Ana  ')).toBe('Ana');
		expect(answerToSend('')).toBe('');
	});

	it('cuts to the server limit in runes, never splitting an emoji', () => {
		expect(MAX_ANSWER_LEN).toBe(40);
		const long = 'ñ'.repeat(50);
		expect(Array.from(answerToSend(long))).toHaveLength(40);
		const emoji = '🍓'.repeat(45);
		const cut = answerToSend(emoji);
		expect(Array.from(cut)).toHaveLength(40);
		expect(cut).toBe('🍓'.repeat(40));
		expect(answerToSend('a'.repeat(40))).toBe('a'.repeat(40));
	});
});
