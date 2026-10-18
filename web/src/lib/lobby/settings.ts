import type { CategoryRef, Lang, Settings } from '$lib/protocol';
import { MAX_CATEGORIES, MIN_CATEGORIES } from './labels';
import { clampRounds } from './letters';

/**
 * Pure builders for `Settings`. `session.updateSettings` sends the WHOLE
 * object, so every change is "the current settings + one edit". A builder
 * returns the SAME object when the edit is a no-op (callers skip sending).
 */
export const TIME_LIMITS = [0, 60, 90, 120, 180] as const;
export const DEFAULT_ROUNDS = 5;

export function withLang(s: Settings, lang: Lang): Settings {
	if (s.lang === lang) return s;
	return { ...s, lang, rounds: clampRounds(s.rounds, lang, s.excludeHardLetters) };
}

export function withExcludeHard(s: Settings, excludeHardLetters: boolean): Settings {
	if (s.excludeHardLetters === excludeHardLetters) return s;
	return { ...s, excludeHardLetters, rounds: clampRounds(s.rounds, s.lang, excludeHardLetters) };
}

/** `rounds` 0 = unlimited; anything else is clamped to 1..(playable letters). */
export function withRounds(s: Settings, rounds: number): Settings {
	const next = clampRounds(rounds, s.lang, s.excludeHardLetters);
	return next === s.rounds ? s : { ...s, rounds: next };
}

export function withTimeLimit(s: Settings, timeLimitSeconds: number): Settings {
	if (!(TIME_LIMITS as readonly number[]).includes(timeLimitSeconds)) return s;
	return timeLimitSeconds === s.timeLimitSeconds ? s : { ...s, timeLimitSeconds };
}

export function withStopVote(s: Settings, stopVote: boolean): Settings {
	return stopVote === s.stopVote ? s : { ...s, stopVote };
}

export function withCategories(s: Settings, categories: CategoryRef[]): Settings {
	return { ...s, categories };
}

export function builtinIds(s: Settings): string[] {
	return s.categories.flatMap((ref) => (ref.id ? [ref.id] : []));
}

export function customLabels(s: Settings): string[] {
	return s.categories.flatMap((ref) => (ref.custom ? [ref.custom] : []));
}

export function hasBuiltin(s: Settings, id: string): boolean {
	return s.categories.some((ref) => ref.id === id);
}

export const canAddCategory = (s: Settings) => s.categories.length < MAX_CATEGORIES;
export const canRemoveCategory = (s: Settings) => s.categories.length > MIN_CATEGORIES;

/** Appends a built-in category (order = field order in the game). No-op when selected or full. */
export function addBuiltin(s: Settings, id: string): Settings {
	if (hasBuiltin(s, id) || !canAddCategory(s)) return s;
	return withCategories(s, [...s.categories, { id }]);
}

/** Appends an already validated custom label. No-op when full. */
export function addCustom(s: Settings, label: string): Settings {
	if (!canAddCategory(s)) return s;
	return withCategories(s, [...s.categories, { custom: label }]);
}

/** Removes the category at `index`. No-op at the 3-category minimum or out of range. */
export function removeCategoryAt(s: Settings, index: number): Settings {
	if (!canRemoveCategory(s) || index < 0 || index >= s.categories.length) return s;
	return withCategories(
		s,
		s.categories.filter((_, i) => i !== index)
	);
}

/** Toggles a built-in category: removes it when selected (respecting the minimum), else appends it. */
export function toggleBuiltin(s: Settings, id: string): Settings {
	const index = s.categories.findIndex((ref) => ref.id === id);
	return index >= 0 ? removeCategoryAt(s, index) : addBuiltin(s, id);
}

/** Moves the category at `index` by `delta` positions (-1 = up, +1 = down). No-op out of range. */
export function moveCategory(s: Settings, index: number, delta: number): Settings {
	const target = index + delta;
	if (
		index < 0 ||
		index >= s.categories.length ||
		target < 0 ||
		target >= s.categories.length ||
		target === index
	) {
		return s;
	}
	const categories = [...s.categories];
	[categories[index], categories[target]] = [categories[target], categories[index]];
	return withCategories(s, categories);
}

/** Structural equality of two settings (order of categories matters). */
export function sameSettings(a: Settings, b: Settings): boolean {
	return (
		a.lang === b.lang &&
		a.excludeHardLetters === b.excludeHardLetters &&
		a.rounds === b.rounds &&
		a.timeLimitSeconds === b.timeLimitSeconds &&
		a.stopVote === b.stopVote &&
		a.categories.length === b.categories.length &&
		a.categories.every(
			(ref, i) =>
				(ref.id ?? '') === (b.categories[i].id ?? '') &&
				(ref.custom ?? '') === (b.categories[i].custom ?? '')
		)
	);
}
