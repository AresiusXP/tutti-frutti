import { answerToSend } from './answer';

export const DEBOUNCE_MS = 300;
/** While the grace countdown runs every keystroke counts: send almost immediately. */
export const GRACE_DEBOUNCE_MS = 100;

type Send = (category: number, text: string) => void;

/**
 * The local answer drafts of one round and the logic that sends them.
 *
 * - `values` is what the inputs show (reactive). It is seeded from the server's
 *   copy of the player's own answers, but once the player edits a field the
 *   local text always wins: a late snapshot never overwrites what is being typed.
 * - A field is *dirty* while its trimmed text differs from what was last sent.
 *   `input` sends it after a debounce, `flush`/`flushAll` right away.
 */
export class AnswerDrafts {
	values = $state<string[]>([]);
	/** Debounce while typing (shorter during the grace countdown). */
	debounceMs = DEBOUNCE_MS;

	#send: Send;
	#sent: string[] = [];
	#touched: boolean[] = [];
	#timers: (ReturnType<typeof setTimeout> | undefined)[] = [];

	constructor(size: number, send: Send, initial: readonly string[] = []) {
		this.#send = send;
		this.reset(size, initial);
	}

	get size(): number {
		return this.values.length;
	}

	/** Starts over with `size` fields seeded from `initial` (a new round). */
	reset(size: number, initial: readonly string[] = []): void {
		this.dispose();
		this.values = Array.from({ length: size }, (_, i) => initial[i] ?? '');
		this.#sent = this.values.map((v) => answerToSend(v));
		this.#touched = this.values.map(() => false);
		this.#timers = this.values.map(() => undefined);
	}

	isDirty(category: number): boolean {
		return answerToSend(this.values[category] ?? '') !== (this.#sent[category] ?? '');
	}

	get dirtyCount(): number {
		return this.values.reduce((n, _, i) => n + (this.isDirty(i) ? 1 : 0), 0);
	}

	/** The player typed: keep it locally and send it after the debounce. */
	input(category: number, text: string): void {
		if (category < 0 || category >= this.values.length) return;
		this.values[category] = text;
		this.#touched[category] = true;
		this.#clear(category);
		if (!this.isDirty(category)) return;
		this.#timers[category] = setTimeout(() => this.flush(category), this.debounceMs);
	}

	/** Sends one field now if it is dirty. */
	flush(category: number): void {
		this.#clear(category);
		if (!this.isDirty(category)) return;
		this.#transmit(category);
	}

	/** Sends every dirty field now, in category order. */
	flushAll(): void {
		for (let i = 0; i < this.values.length; i++) this.flush(i);
	}

	/**
	 * A fresh snapshot of the player's own answers arrived. Fields the player has
	 * not touched follow the server (a reload, a reconnect to a newer state);
	 * touched ones keep the local text.
	 */
	adopt(server: readonly string[]): void {
		for (let i = 0; i < this.values.length; i++) {
			if (this.#touched[i]) continue;
			const text = server[i] ?? '';
			if (this.values[i] !== text) this.values[i] = text;
			this.#sent[i] = answerToSend(text);
		}
	}

	/**
	 * After a reconnect the server may be behind (a restart restores the last
	 * snapshot): untouched fields adopt its copy, touched ones whose text it does
	 * not have are sent again.
	 */
	resync(server: readonly string[]): void {
		this.adopt(server);
		for (let i = 0; i < this.values.length; i++) {
			if (!this.#touched[i]) continue;
			this.#clear(i);
			const mine = answerToSend(this.values[i]);
			this.#sent[i] = server[i] ?? '';
			if (mine !== this.#sent[i]) this.#transmit(i);
		}
	}

	/** Cancels the pending sends (the screen is going away; nothing is flushed on purpose). */
	dispose(): void {
		for (const timer of this.#timers) if (timer !== undefined) clearTimeout(timer);
		this.#timers = this.#timers.map(() => undefined);
	}

	#transmit(category: number): void {
		const text = answerToSend(this.values[category]);
		this.#sent[category] = text;
		this.#send(category, text);
	}

	#clear(category: number): void {
		const timer = this.#timers[category];
		if (timer !== undefined) clearTimeout(timer);
		this.#timers[category] = undefined;
	}
}
