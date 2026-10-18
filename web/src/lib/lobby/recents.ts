import { cleanLabel, MAX_CUSTOM_LEN, normalizeLabel, runeCount } from './labels';

/** The host's most recent custom categories, remembered across games. */
export const RECENTS_KEY = 'tf.customs';
export const RECENTS_MAX = 10;

export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

function browserStorage(): StorageLike | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

// Language-neutral comparison of the remembered list (ñ folds to n, ĳ to ij, case and accents ignored).
const key = (label: string) => normalizeLabel(label, 'en');

/** Reads the list (newest first). Corrupted or blocked storage gives `[]`; it never throws. */
export function loadRecents(storage: StorageLike | null = browserStorage()): string[] {
	try {
		const parsed: unknown = JSON.parse(storage?.getItem(RECENTS_KEY) ?? '[]');
		if (!Array.isArray(parsed)) return [];
		const out: string[] = [];
		for (const item of parsed) {
			if (typeof item !== 'string') continue;
			const label = cleanLabel(item);
			if (label === '' || runeCount(label) > MAX_CUSTOM_LEN) continue;
			if (out.some((other) => key(other) === key(label))) continue;
			out.push(label);
		}
		return out.slice(0, RECENTS_MAX);
	} catch {
		return [];
	}
}

function save(list: string[], storage: StorageLike | null): void {
	try {
		storage?.setItem(RECENTS_KEY, JSON.stringify(list));
	} catch {
		// full or blocked storage: the in-memory list still works
	}
}

/** Puts `label` first (deduplicated, at most 10) and stores it. Returns the new list. */
export function rememberCustom(
	label: string,
	storage: StorageLike | null = browserStorage()
): string[] {
	const clean = cleanLabel(label);
	if (clean === '') return loadRecents(storage);
	const list = [clean, ...loadRecents(storage).filter((other) => key(other) !== key(clean))].slice(
		0,
		RECENTS_MAX
	);
	save(list, storage);
	return list;
}

/** Removes `label` from the list and stores it. Returns the new list. */
export function forgetCustom(
	label: string,
	storage: StorageLike | null = browserStorage()
): string[] {
	const list = loadRecents(storage).filter((other) => key(other) !== key(label));
	save(list, storage);
	return list;
}
