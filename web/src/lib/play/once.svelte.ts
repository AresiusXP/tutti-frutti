import type { SessionError } from '$lib/game/session';

/**
 * Guard that makes a command impossible to fire twice (STOP, BASTA).
 *
 * It locks on the first `fire` and stays locked until the room changes phase
 * (the screen goes away) or the session reports a NEW error for that command
 * (`failed`), or, optionally, after `retryAfterMs` (a lost command must not
 * leave the button dead forever).
 */
export class OnceCommand {
	locked = $state(false);

	#refs: readonly string[];
	#retryAfterMs: number;
	#baseSeq = 0;
	#timer: ReturnType<typeof setTimeout> | undefined;

	constructor(refs: readonly string[], retryAfterMs = 0) {
		this.#refs = refs;
		this.#retryAfterMs = retryAfterMs;
	}

	/** Runs `run` unless already locked. `errorSeq` is the seq of the session's latest error right now. */
	fire(errorSeq: number, run: () => void): boolean {
		if (this.locked) return false;
		this.locked = true;
		this.#baseSeq = errorSeq;
		if (this.#retryAfterMs > 0) {
			this.#timer = setTimeout(() => this.unlock(), this.#retryAfterMs);
		}
		run();
		return true;
	}

	/** Feed it `session.lastError`: a new error for this command re-arms it. */
	failed(error: SessionError | null): void {
		if (!this.locked || !error || error.seq <= this.#baseSeq) return;
		if (error.ref === undefined || this.#refs.includes(error.ref)) this.unlock();
	}

	unlock(): void {
		clearTimeout(this.#timer);
		this.#timer = undefined;
		this.locked = false;
	}

	dispose(): void {
		clearTimeout(this.#timer);
		this.#timer = undefined;
	}
}
