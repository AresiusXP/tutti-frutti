import { SvelteSet } from 'svelte/reactivity';

/**
 * First-time tips ("Tip" cards on the playing and review screens). A tip is shown once per browser:
 * the ids of the tips the player has seen are kept in localStorage under `tf.tips` (a JSON array,
 * nothing else), so a returning player is not told the same thing every round. Corrupt data and
 * unavailable storage are tolerated and never throw: the tip is then simply shown again.
 */
export const TIPS_KEY = 'tf.tips';
export const TIP_IDS = ['playing', 'review'] as const;
export type TipId = (typeof TIP_IDS)[number];

export type TipStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

const isTipId = (value: unknown): value is TipId =>
	typeof value === 'string' && (TIP_IDS as readonly string[]).includes(value);

function browserStorage(): TipStorage | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

export class TipsStore {
	#storage: TipStorage | null;
	#seen = new SvelteSet<TipId>();

	constructor(storage: TipStorage | null = browserStorage()) {
		this.#storage = storage;
		try {
			const stored: unknown = JSON.parse(storage?.getItem(TIPS_KEY) ?? 'null');
			if (Array.isArray(stored)) for (const id of stored) if (isTipId(id)) this.#seen.add(id);
		} catch {
			// corrupted or blocked storage: every tip is shown once more
		}
	}

	/** The player has already seen (closed or left behind) this tip. */
	seen(id: TipId): boolean {
		return this.#seen.has(id);
	}

	/** Remembers that the tip was seen. */
	markSeen(id: TipId): void {
		if (this.#seen.has(id)) return;
		this.#seen.add(id);
		this.#save();
	}

	/** Forgets everything: every tip shows again (tests and the dev playground). */
	reset(): void {
		this.#seen.clear();
		try {
			this.#storage?.removeItem(TIPS_KEY);
		} catch {
			// nothing to forget
		}
	}

	#save() {
		try {
			this.#storage?.setItem(TIPS_KEY, JSON.stringify([...this.#seen]));
		} catch {
			// full or blocked storage: the tip stays hidden until the page is reloaded
		}
	}
}

export const tips = new TipsStore();
