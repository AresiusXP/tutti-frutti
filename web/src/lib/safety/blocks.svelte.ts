/**
 * The caller's block list (docs/DESIGN.md, "Content safety"; the server owns it, keyed by the guest cookie).
 * This store is only a cache of `GET /api/blocks` for the UI: it is never persisted in the browser (the
 * privacy policy lists exactly which keys the app keeps, and a list of nicknames is not one of them), so
 * a reload asks the server again.
 *
 * `expecting` is the room the server is about to remove the player from after "Block and leave": the
 * socket may close (4005/4002/4004) before or after the HTTP answer, and either way the room route
 * must go Home quietly instead of showing "you were kicked".
 */
import { ApiError } from '$lib/api';
import type { BlockedPlayer } from '$lib/protocol/api';
import { type SafetyApi, safetyApi } from './api';

export type BlockOutcome = { ok: true; leftRooms: string[] } | { ok: false; code: string };
export type UnblockOutcome = { ok: true } | { ok: false; code: string };

const codeOf = (e: unknown): string => (e instanceof ApiError ? e.code : 'unknown');

export class BlocksStore {
	list = $state.raw<BlockedPlayer[]>([]);
	status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
	error = $state<string | null>(null);
	/** Room code of a "Block and leave" in flight or just done (see the file header). */
	expecting = $state<string | null>(null);

	readonly #api: () => SafetyApi;
	#seq = 0;

	constructor(api?: SafetyApi) {
		this.#api = () => api ?? safetyApi;
	}

	isBlocked(playerId: string): boolean {
		return this.list.some((b) => b.playerId === playerId);
	}

	/** Refreshes the list; the previous one stays on screen meanwhile and after a failure. */
	async load(): Promise<boolean> {
		const mine = ++this.#seq;
		if (this.status !== 'ready') this.status = 'loading';
		this.error = null;
		try {
			const blocks = await this.#api().blocks();
			if (mine !== this.#seq) return true;
			this.list = blocks;
			this.status = 'ready';
			return true;
		} catch (e) {
			if (mine !== this.#seq) return false;
			this.error = codeOf(e);
			this.status = this.list.length > 0 ? 'ready' : 'error';
			return false;
		}
	}

	/** `POST /api/blocks`. On success the block is remembered here, newest first. */
	async block(player: { id: string; name: string }): Promise<BlockOutcome> {
		try {
			const res = await this.#api().block({ playerId: player.id, name: player.name });
			this.#seq++;
			this.list = [res.block, ...this.list.filter((b) => b.playerId !== res.block.playerId)];
			if (this.status !== 'ready') this.status = 'ready';
			return { ok: true, leftRooms: res.leftRooms };
		} catch (e) {
			return { ok: false, code: codeOf(e) };
		}
	}

	/** `DELETE /api/blocks/{playerId}`; the entry leaves the list only once the server said so. */
	async unblock(playerId: string): Promise<UnblockOutcome> {
		try {
			await this.#api().unblock(playerId);
			this.#seq++;
			this.list = this.list.filter((b) => b.playerId !== playerId);
			return { ok: true };
		} catch (e) {
			return { ok: false, code: codeOf(e) };
		}
	}

	expectRemoval(code: string | null): void {
		this.expecting = code;
	}
}

export const blocks = new BlocksStore();
