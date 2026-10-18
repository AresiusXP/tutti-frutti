import { type Avatar, DEFAULT_AVATAR, isAvatar, randomAvatar } from './avatar';
import { normalizeNickname } from './nickname';

export const NICKNAME_KEY = 'tf.nickname';
export const AVATAR_KEY = 'tf.avatar';

export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

function browserStorage(): StorageLike | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

function write(storage: StorageLike | null, key: string, value: string) {
	try {
		storage?.setItem(key, value);
	} catch {
		// storage can be full or blocked (private mode); the in-memory value still works
	}
}

/** Nickname + avatar of the local player, remembered in localStorage. */
export class ProfileStore {
	#storage: StorageLike | null;
	nickname = $state('');
	avatar = $state<Avatar>({ ...DEFAULT_AVATAR });

	/**
	 * `randomDefault`: a player without a saved avatar gets a random one (and it
	 * is remembered) instead of the fixed default. Off by default so tests stay
	 * deterministic; the app's singleton turns it on.
	 */
	constructor(
		storage: StorageLike | null = browserStorage(),
		options: { randomDefault?: boolean; rng?: () => number } = {}
	) {
		this.#storage = storage;
		let saved = false;
		try {
			this.nickname = normalizeNickname(storage?.getItem(NICKNAME_KEY) ?? '');
			const stored: unknown = JSON.parse(storage?.getItem(AVATAR_KEY) ?? 'null');
			if (isAvatar(stored)) {
				this.avatar = stored;
				saved = true;
			}
		} catch {
			// corrupted storage: keep defaults
		}
		if (!saved && options.randomDefault) {
			this.avatar = randomAvatar(options.rng);
			write(storage, AVATAR_KEY, JSON.stringify(this.avatar));
		}
	}

	/** Stores the trimmed nickname (max 16 chars) and returns the stored value. */
	setNickname(raw: string): string {
		this.nickname = normalizeNickname(raw);
		write(this.#storage, NICKNAME_KEY, this.nickname);
		return this.nickname;
	}

	setAvatar(next: Partial<Avatar>) {
		const merged = { ...this.avatar, ...next };
		if (!isAvatar(merged)) return;
		this.avatar = merged;
		write(this.#storage, AVATAR_KEY, JSON.stringify(merged));
	}
}

export const profile = new ProfileStore(undefined, { randomDefault: true });
