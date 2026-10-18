/**
 * `tf.room.<CODE>` = '1' in sessionStorage: this TAB already chose to be in
 * that room (it created it, joined from Home, or pressed Join on the gate), so
 * the room route connects without asking again (reloads included). It is
 * per-tab on purpose: an invite link opened in a fresh tab always shows the gate.
 */
const PREFIX = 'tf.room.';

type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function store(): Store | null {
	try {
		return typeof sessionStorage === 'undefined' ? null : sessionStorage;
	} catch {
		return null;
	}
}

export function roomMarkKey(code: string): string {
	return PREFIX + code.toUpperCase();
}

export function markRoom(code: string, storage: Store | null = store()): void {
	try {
		storage?.setItem(roomMarkKey(code), '1');
	} catch {
		// private mode / full storage: the gate will simply show again after a reload
	}
}

export function hasRoomMark(code: string, storage: Store | null = store()): boolean {
	try {
		return storage?.getItem(roomMarkKey(code)) === '1';
	} catch {
		return false;
	}
}

export function clearRoomMark(code: string, storage: Store | null = store()): void {
	try {
		storage?.removeItem(roomMarkKey(code));
	} catch {
		// nothing to clean
	}
}
