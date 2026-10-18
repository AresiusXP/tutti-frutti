import { describe, expect, it } from 'vitest';
import { forgetCustom, loadRecents, RECENTS_KEY, RECENTS_MAX, rememberCustom } from './recents';

function memory(initial?: string) {
	const data = new Map<string, string>();
	if (initial !== undefined) data.set(RECENTS_KEY, initial);
	return {
		data,
		getItem: (key: string) => data.get(key) ?? null,
		setItem: (key: string, value: string) => void data.set(key, value)
	};
}

const blocked = {
	getItem: () => {
		throw new Error('SecurityError');
	},
	setItem: () => {
		throw new Error('QuotaExceededError');
	}
};

describe('recents', () => {
	it('starts empty and remembers the newest first', () => {
		const storage = memory();
		expect(loadRecents(storage)).toEqual([]);
		rememberCustom('Algo que da risa', storage);
		expect(rememberCustom('Excusas', storage)).toEqual(['Excusas', 'Algo que da risa']);
		expect(JSON.parse(storage.data.get(RECENTS_KEY)!)).toEqual(['Excusas', 'Algo que da risa']);
		expect(loadRecents(storage)).toEqual(['Excusas', 'Algo que da risa']);
	});

	it('moves a repeated label to the front, ignoring case and accents', () => {
		const storage = memory();
		rememberCustom('Año nuevo', storage);
		rememberCustom('Otra', storage);
		expect(rememberCustom('  AÑO   nuevo ', storage)).toEqual(['AÑO nuevo', 'Otra']);
	});

	it('keeps only the last 10', () => {
		const storage = memory();
		for (let i = 1; i <= 12; i++) rememberCustom(`Cosa ${i}`, storage);
		const list = loadRecents(storage);
		expect(list).toHaveLength(RECENTS_MAX);
		expect(list[0]).toBe('Cosa 12');
		expect(list.at(-1)).toBe('Cosa 3');
	});

	it('ignores blank labels', () => {
		const storage = memory();
		expect(rememberCustom('   ', storage)).toEqual([]);
		expect(storage.data.has(RECENTS_KEY)).toBe(false);
	});

	it('forgets one entry', () => {
		const storage = memory();
		rememberCustom('Uno', storage);
		rememberCustom('Dos', storage);
		expect(forgetCustom('uno', storage)).toEqual(['Dos']);
		expect(loadRecents(storage)).toEqual(['Dos']);
		expect(forgetCustom('nada', storage)).toEqual(['Dos']);
	});

	it('survives corrupted data', () => {
		expect(loadRecents(memory('{not json'))).toEqual([]);
		expect(loadRecents(memory('{"a":1}'))).toEqual([]);
		expect(
			loadRecents(memory(JSON.stringify([1, null, '', '  ', 'Ok', 'ok', 'x'.repeat(30), 'Bien'])))
		).toEqual(['Ok', 'Bien']);
	});

	it('never throws when storage is blocked or missing', () => {
		expect(loadRecents(blocked)).toEqual([]);
		expect(rememberCustom('Algo', blocked)).toEqual(['Algo']);
		expect(forgetCustom('Algo', blocked)).toEqual([]);
		expect(loadRecents(null)).toEqual([]);
		expect(rememberCustom('Algo', null)).toEqual(['Algo']);
		expect(forgetCustom('Algo', null)).toEqual([]);
	});

	it('uses localStorage by default and survives it being unavailable', () => {
		expect(() => loadRecents()).not.toThrow();
		expect(() => rememberCustom('Algo')).not.toThrow();
		expect(() => forgetCustom('Algo')).not.toThrow();
	});
});
