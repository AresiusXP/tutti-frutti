import { type Avatar, DEFAULT_AVATAR, isAvatar } from './avatar';
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

	constructor(storage: StorageLike | null = browserStorage()) {
		this.#storage = storage;
		try {
			this.nickname = normalizeNickname(storage?.getItem(NICKNAME_KEY) ?? '');
			const saved: unknown = JSON.parse(storage?.getItem(AVATAR_KEY) ?? 'null');
			if (isAvatar(saved)) this.avatar = saved;
		} catch {
			// corrupted storage: keep defaults
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

export const profile = new ProfileStore();
