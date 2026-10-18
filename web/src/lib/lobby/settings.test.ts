import { describe, expect, it } from 'vitest';
import { makeSettings } from '$lib/game/fixtures';
import {
	addBuiltin,
	addCustom,
	builtinIds,
	canAddCategory,
	canRemoveCategory,
	customLabels,
	hasBuiltin,
	moveCategory,
	removeCategoryAt,
	sameSettings,
	TIME_LIMITS,
	toggleBuiltin,
	withCategories,
	withExcludeHard,
	withLang,
	withRounds,
	withStopVote,
	withTimeLimit
} from './settings';

const ids = (s: { categories: { id?: string; custom?: string }[] }) =>
	s.categories.map((c) => c.id ?? c.custom);

describe('scalar settings', () => {
	it('changes the language and keeps everything else, clamping rounds', () => {
		const s = makeSettings({ excludeHardLetters: false, rounds: 27 });
		expect(withLang(s, 'en')).toEqual({ ...s, lang: 'en', rounds: 26 });
		expect(withLang(s, 'es')).toBe(s);
		expect(withLang(makeSettings({ rounds: 0 }), 'en').rounds).toBe(0);
	});

	it('changes to Dutch and clamps rounds to its 26 / 23 letters', () => {
		const all = makeSettings({ excludeHardLetters: false, rounds: 27 });
		expect(withLang(all, 'nl')).toEqual({ ...all, lang: 'nl', rounds: 26 });
		const easy = makeSettings({ excludeHardLetters: true, rounds: 20 });
		expect(withLang(easy, 'nl')).toEqual({ ...easy, lang: 'nl' });
		expect(withExcludeHard(withLang(all, 'nl'), true).rounds).toBe(23);
		expect(withRounds(withLang(easy, 'nl'), 99).rounds).toBe(23);
		expect(withLang(withLang(all, 'nl'), 'es').rounds).toBe(26);
	});

	it('toggles hard letters and clamps rounds when they are excluded', () => {
		const s = makeSettings({ excludeHardLetters: false, rounds: 27 });
		expect(withExcludeHard(s, true)).toEqual({ ...s, excludeHardLetters: true, rounds: 20 });
		expect(withExcludeHard(s, false)).toBe(s);
		expect(withExcludeHard(makeSettings({ rounds: 5 }), false).rounds).toBe(5);
	});

	it('sets rounds within 1..max and 0 for unlimited', () => {
		const s = makeSettings({ rounds: 5 });
		expect(withRounds(s, 8).rounds).toBe(8);
		expect(withRounds(s, 99).rounds).toBe(20);
		expect(withRounds(s, 0).rounds).toBe(0);
		expect(withRounds(s, 5)).toBe(s);
	});

	it('only accepts the allowed time limits', () => {
		const s = makeSettings();
		expect(TIME_LIMITS).toEqual([0, 60, 90, 120, 180]);
		expect(withTimeLimit(s, 90).timeLimitSeconds).toBe(90);
		expect(withTimeLimit(s, 45)).toBe(s);
		expect(withTimeLimit(s, 0)).toBe(s);
	});
});

describe('stopVote', () => {
	it('toggles the vote to stop and is a no-op (same object) when unchanged', () => {
		const s = makeSettings();
		expect(s.stopVote).toBe(true);
		expect(withStopVote(s, true)).toBe(s);
		expect(withStopVote(s, false)).toEqual({ ...s, stopVote: false });
		expect(withStopVote(withStopVote(s, false), true)).toEqual(s);
	});

	it('every other edit carries it along (an update_settings without it would turn the feature off)', () => {
		for (const stopVote of [true, false]) {
			const s = makeSettings({ stopVote, excludeHardLetters: false, rounds: 27 });
			const edits = [
				withLang(s, 'en'),
				withLang(s, 'nl'),
				withExcludeHard(s, true),
				withRounds(s, 3),
				withTimeLimit(s, 90),
				withCategories(s, [{ id: 'nombre' }, { id: 'pais' }, { id: 'animal' }]),
				addBuiltin(s, 'ciudad'),
				addCustom(s, 'Algo'),
				removeCategoryAt(s, 0),
				toggleBuiltin(s, 'nombre'),
				moveCategory(s, 0, 1)
			];
			for (const edit of edits) expect(edit.stopVote, JSON.stringify(edit)).toBe(stopVote);
		}
	});
});

describe('category builders', () => {
	const base = makeSettings();

	it('appends built-ins in order and ignores duplicates', () => {
		const next = addBuiltin(base, 'ciudad');
		expect(ids(next)).toEqual([...ids(base), 'ciudad']);
		expect(addBuiltin(next, 'ciudad')).toBe(next);
		expect(hasBuiltin(next, 'ciudad')).toBe(true);
		expect(hasBuiltin(base, 'ciudad')).toBe(false);
	});

	it('appends customs and lists ids / customs', () => {
		const next = addCustom(addBuiltin(base, 'ciudad'), 'Algo que da risa');
		expect(next.categories.at(-1)).toEqual({ custom: 'Algo que da risa' });
		expect(customLabels(next)).toEqual(['Algo que da risa']);
		expect(builtinIds(next)).toEqual([...ids(base), 'ciudad']);
	});

	it('stops at 10 categories', () => {
		let s = base;
		for (const id of ['a1', 'a2', 'a3', 'a4', 'a5']) s = addBuiltin(s, id);
		expect(s.categories).toHaveLength(10);
		expect(canAddCategory(s)).toBe(false);
		expect(addBuiltin(s, 'zz')).toBe(s);
		expect(addCustom(s, 'Zz')).toBe(s);
	});

	it('stops at 3 categories', () => {
		const three = withCategories(base, base.categories.slice(0, 3));
		expect(canRemoveCategory(three)).toBe(false);
		expect(removeCategoryAt(three, 0)).toBe(three);
		expect(toggleBuiltin(three, three.categories[0].id!)).toBe(three);
		expect(canRemoveCategory(base)).toBe(true);
	});

	it('removes by index and by toggle, ignoring bad indexes', () => {
		expect(ids(removeCategoryAt(base, 1))).toEqual([
			'nombre',
			'animal',
			'comida',
			'color',
			'marca'
		]);
		expect(removeCategoryAt(base, -1)).toBe(base);
		expect(removeCategoryAt(base, 6)).toBe(base);
		expect(ids(toggleBuiltin(base, 'pais'))).not.toContain('pais');
		expect(ids(toggleBuiltin(base, 'ciudad'))).toContain('ciudad');
	});

	it('moves categories up and down within bounds', () => {
		expect(ids(moveCategory(base, 1, -1)).slice(0, 3)).toEqual(['pais', 'nombre', 'animal']);
		expect(ids(moveCategory(base, 0, 1)).slice(0, 3)).toEqual(['pais', 'nombre', 'animal']);
		expect(moveCategory(base, 0, -1)).toBe(base);
		expect(moveCategory(base, 5, 1)).toBe(base);
		expect(moveCategory(base, 9, -1)).toBe(base);
		expect(moveCategory(base, 2, 0)).toBe(base);
		expect(base.categories[0].id).toBe('nombre');
	});
});

describe('sameSettings', () => {
	it('compares every field and the category order', () => {
		const a = makeSettings();
		expect(sameSettings(a, makeSettings())).toBe(true);
		expect(sameSettings(a, makeSettings({ rounds: 6 }))).toBe(false);
		expect(sameSettings(a, makeSettings({ lang: 'en' }))).toBe(false);
		expect(sameSettings(a, makeSettings({ excludeHardLetters: false }))).toBe(false);
		expect(sameSettings(a, makeSettings({ timeLimitSeconds: 60 }))).toBe(false);
		expect(sameSettings(a, makeSettings({ stopVote: false }))).toBe(false);
		expect(sameSettings(a, moveCategory(a, 0, 1))).toBe(false);
		expect(sameSettings(a, addBuiltin(a, 'ciudad'))).toBe(false);
		expect(sameSettings(addCustom(a, 'X'), addCustom(a, 'X'))).toBe(true);
		expect(sameSettings(addCustom(a, 'X'), addCustom(a, 'Y'))).toBe(false);
	});
});
