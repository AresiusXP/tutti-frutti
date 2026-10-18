/**
 * Pure countdown helpers. Every function takes `now` (ms since the epoch, from
 * `session.serverNow()`) so tests never depend on the real clock.
 */

/** Length of the grace countdown after a Basta call (`game.GraceDuration`). */
export const GRACE_MS = 3000;
/** How long the full-screen Basta splash stays before it collapses into the banner. */
export const SPLASH_MS = 900;
/** Last seconds of a time limit: tangerine first, strawberry at the end. */
export const WARN_SECONDS = 10;
export const DANGER_SECONDS = 5;

const ZERO_TIME = Date.parse('0001-01-01T00:00:00Z');

/** Parses a deadline of the view; the zero time (`0001-01-01...`), missing or invalid values mean "unset" (`null`). */
export function parseDeadline(iso: string | null | undefined): number | null {
	if (!iso) return null;
	const ms = Date.parse(iso);
	return Number.isFinite(ms) && ms > ZERO_TIME ? ms : null;
}

/** Whole seconds left, rounded up (so "1" shows until the very last millisecond) and never negative. */
export function secondsLeft(deadline: number, now: number): number {
	return Math.max(0, Math.ceil((deadline - now) / 1000));
}

/** `m:ss` for a number of seconds. */
export function formatClock(seconds: number): string {
	const s = Math.max(0, Math.floor(seconds));
	return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

export type TimerTone = 'calm' | 'warn' | 'danger';

export function timerTone(seconds: number): TimerTone {
	if (seconds <= DANGER_SECONDS) return 'danger';
	if (seconds <= WARN_SECONDS) return 'warn';
	return 'calm';
}

/**
 * Whether a screen that appears while the room is already in grace should still
 * play the Basta splash: only during the first `SPLASH_MS` of the countdown
 * (a reload 2 s into the grace goes straight to the banner).
 */
export function splashStillRelevant(graceEndsAt: number | null, now: number): boolean {
	if (graceEndsAt === null) return false;
	return graceEndsAt - now > GRACE_MS - SPLASH_MS;
}
