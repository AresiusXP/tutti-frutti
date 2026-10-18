import { describe, expect, it } from 'vitest';
import {
	ROOM_CODE_ALPHABET,
	backspaceAt,
	isCompleteRoomCode,
	isRoomCodeChar,
	normalizePastedCode,
	sanitizeRoomCode,
	typeIntoBox
} from './roomCode';

describe('alphabet', () => {
	it('is the 28-character vowel-free, look-alike-free set', () => {
		expect(ROOM_CODE_ALPHABET).toBe('BCDFGHJKLMNPQRSTVWXZ23456789');
		expect(ROOM_CODE_ALPHABET).toHaveLength(28);
	});

	it.each([...ROOM_CODE_ALPHABET])('accepts %s', (ch) => {
		expect(isRoomCodeChar(ch)).toBe(true);
		expect(isRoomCodeChar(ch.toLowerCase())).toBe(true);
	});

	it.each([
		'A',
		'E',
		'I',
		'O',
		'U',
		'Y',
		'0',
		'1',
		'a',
		'o',
		'i',
		'-',
		' ',
		'Ñ',
		'ß',
		'ſ',
		'ı',
		'\u212A', // Kelvin sign
		'',
		'KT'
	])('rejects %j', (ch) => {
		expect(isRoomCodeChar(ch)).toBe(false);
	});
});

describe('sanitizeRoomCode', () => {
	it.each([
		['kt7p', 'KT7P'],
		['KT7P', 'KT7P'],
		[' k t 7 p ', 'KT7P'],
		['k-t-7-p', 'KT7P'],
		['KT7PXZ', 'KT7P'],
		['AEIOU', ''],
		['0123', '23'],
		['b0c1d2f3', 'BCD2'],
		['', ''],
		// ASCII-only upper-casing: "ß" must not expand to "SS", "ſ" must not become "S"
		['ß', ''],
		['ßß', ''],
		['ſſ7', '7'],
		['ﬁ', '']
	])('%j -> %j', (input, expected) => {
		expect(sanitizeRoomCode(input)).toBe(expected);
	});
});

describe('normalizePastedCode', () => {
	describe('accepts bare codes', () => {
		it.each([
			['kt7p', 'KT7P'],
			['KT7P', 'KT7P'],
			['  KT7P\n', 'KT7P'],
			['\tkt7p ', 'KT7P'],
			['kt 7p', 'KT7P'],
			['KT 7P', 'KT7P'],
			['K-T7P', 'KT7P'],
			['k - t - 7 - p', 'KT7P'],
			['KT-7P', 'KT7P'],
			['2222', '2222']
		])('%j -> %j', (input, expected) => {
			expect(normalizePastedCode(input)).toBe(expected);
		});
	});

	describe('accepts invite links whose path ends in a valid code', () => {
		it.each([
			['https://tuttifrutti.aresius.xyz/KT7P', 'KT7P'],
			['https://tuttifrutti.aresius.xyz/kt7p', 'KT7P'],
			['https://tuttifrutti.aresius.xyz/KT7P/', 'KT7P'],
			['https://tuttifrutti.aresius.xyz/kt7p?utm=1#x', 'KT7P'],
			['https://tuttifrutti.aresius.xyz/KT7P/?utm=1', 'KT7P'],
			['  http://localhost:5173/KT7P/  ', 'KT7P'],
			['https://example.com/join/bcd2', 'BCD2']
		])('%j -> %j', (input, expected) => {
			expect(normalizePastedCode(input)).toBe(expected);
		});
	});

	describe('rejects everything else with an empty result', () => {
		it.each([
			// prose must not be scavenged for letters ("not a code" used to give "NTCD")
			['not a code'],
			['join my game, code is KT7P'],
			['codigo: KT7P'],
			['hola'],
			['KT7P KT7P'],
			// invalid characters / wrong length
			['AEIO'],
			['KT7A'],
			['K0T1'],
			['KT7'],
			['KT7PX'],
			['KT7P2'],
			['K-T-7-P-X'],
			['BCDFGHJK'],
			['0123'],
			['KT7P!'],
			['K.T.7.P'],
			// unicode that upper-cases into valid characters
			['ß'],
			['ßß'],
			['ßßKT'],
			['ſſkt'],
			['ﬁ7P'],
			['\u212AT7P'], // Kelvin sign instead of K
			// links that do not end in a valid code
			['https://tuttifrutti.aresius.xyz/'],
			['https://tuttifrutti.aresius.xyz'],
			['https://tuttifrutti.aresius.xyz/AEIO'],
			['https://tuttifrutti.aresius.xyz/KT7PX'],
			['https://tuttifrutti.aresius.xyz/KT7'],
			['https://tuttifrutti.aresius.xyz/KT7P/extra'],
			['https://tuttifrutti.aresius.xyz/?code=KT7P'],
			['https://tuttifrutti.aresius.xyz/#KT7P'],
			['https://tuttifrutti.aresius.xyz/K-T7P'],
			['https://tuttifrutti.aresius.xyz/KT7P and more'],
			// not http(s) / not a URL
			['ftp://tuttifrutti.aresius.xyz/KT7P'],
			['javascript:KT7P'],
			['localhost:5173/KT7P'],
			['https://'],
			// blank
			[''],
			['   '],
			['---']
		])('%j -> ""', (input) => {
			expect(normalizePastedCode(input)).toBe('');
		});
	});
});

describe('isCompleteRoomCode', () => {
	it.each([
		['KT7P', true],
		['2222', true],
		['KT7', false],
		['KT7PX', false],
		['KT7A', false],
		['kt7p', false],
		['', false]
	])('%j -> %s', (input, expected) => {
		expect(isCompleteRoomCode(input)).toBe(expected);
	});
});

describe('typeIntoBox', () => {
	it('appends a valid character and advances', () => {
		expect(typeIntoBox('', 0, 'k')).toEqual({ value: 'K', focus: 1 });
		expect(typeIntoBox('K', 1, 't')).toEqual({ value: 'KT', focus: 2 });
	});

	it('stays on the last box after the fourth character', () => {
		expect(typeIntoBox('KT7', 3, 'p')).toEqual({ value: 'KT7P', focus: 3 });
	});

	it('ignores vowels, 0, 1, O and I without moving', () => {
		for (const bad of ['a', 'E', 'i', 'O', 'u', '0', '1', '!', ' ']) {
			expect(typeIntoBox('K', 1, bad)).toEqual({ value: 'K', focus: 1 });
		}
	});

	it('never leaves a gap: typing in a far box appends at the end', () => {
		expect(typeIntoBox('K', 3, 'T')).toEqual({ value: 'KT', focus: 2 });
		expect(typeIntoBox('', 2, 'T')).toEqual({ value: 'T', focus: 1 });
	});

	it('overwrites a filled box when the browser reports old+new or new+old', () => {
		expect(typeIntoBox('KT', 0, 'KB')).toEqual({ value: 'BT', focus: 1 });
		expect(typeIntoBox('KT', 0, 'BK')).toEqual({ value: 'BT', focus: 1 });
		expect(typeIntoBox('KT', 1, 'TT')).toEqual({ value: 'KT', focus: 2 });
	});

	it('spreads a multi-character burst (autofill) over the following boxes', () => {
		expect(typeIntoBox('', 0, 'kt7p')).toEqual({ value: 'KT7P', focus: 3 });
		expect(typeIntoBox('K', 1, 't7')).toEqual({ value: 'KT7', focus: 3 });
	});
});

describe('backspaceAt', () => {
	it('clears a filled box and keeps focus', () => {
		expect(backspaceAt('KT7P', 3)).toEqual({ value: 'KT7', focus: 3 });
		expect(backspaceAt('KT7P', 1)).toEqual({ value: 'K7P', focus: 1 });
	});

	it('moves back and clears the previous box when the current one is empty', () => {
		expect(backspaceAt('KT', 2)).toEqual({ value: 'K', focus: 1 });
		expect(backspaceAt('K', 3)).toEqual({ value: '', focus: 0 });
	});

	it('does nothing on an empty first box', () => {
		expect(backspaceAt('', 0)).toEqual({ value: '', focus: 0 });
	});
});
