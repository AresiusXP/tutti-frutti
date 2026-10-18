import { describe, expect, it } from 'vitest';
import {
	GRACE_MS,
	SPLASH_MS,
	formatClock,
	parseDeadline,
	secondsLeft,
	splashStillRelevant,
	timerTone
} from './time';

const NOW = Date.parse('2026-05-01T12:00:00Z');
const at = (ms: number) => new Date(NOW + ms).toISOString();

describe('parseDeadline', () => {
	it('parses ISO strings', () => {
		expect(parseDeadline(at(3000))).toBe(NOW + 3000);
	});

	it('treats missing, empty, zero and invalid values as unset', () => {
		expect(parseDeadline(undefined)).toBeNull();
		expect(parseDeadline(null)).toBeNull();
		expect(parseDeadline('')).toBeNull();
		expect(parseDeadline('0001-01-01T00:00:00Z')).toBeNull();
		expect(parseDeadline('not a date')).toBeNull();
	});
});

describe('secondsLeft', () => {
	it('rounds up so the last second shows 1 until the deadline', () => {
		expect(secondsLeft(NOW + 3000, NOW)).toBe(3);
		expect(secondsLeft(NOW + 2999, NOW)).toBe(3);
		expect(secondsLeft(NOW + 2000, NOW)).toBe(2);
		expect(secondsLeft(NOW + 1, NOW)).toBe(1);
		expect(secondsLeft(NOW, NOW)).toBe(0);
	});

	it('never goes negative', () => {
		expect(secondsLeft(NOW - 5000, NOW)).toBe(0);
	});
});

describe('formatClock', () => {
	it('formats minutes and seconds', () => {
		expect(formatClock(0)).toBe('0:00');
		expect(formatClock(9)).toBe('0:09');
		expect(formatClock(75)).toBe('1:15');
		expect(formatClock(180)).toBe('3:00');
		expect(formatClock(-3)).toBe('0:00');
	});
});

describe('timerTone', () => {
	it('is calm, then tangerine in the last 10 s, then strawberry in the last 5 s', () => {
		expect(timerTone(60)).toBe('calm');
		expect(timerTone(11)).toBe('calm');
		expect(timerTone(10)).toBe('warn');
		expect(timerTone(6)).toBe('warn');
		expect(timerTone(5)).toBe('danger');
		expect(timerTone(0)).toBe('danger');
	});
});

describe('splashStillRelevant', () => {
	it('plays the splash during the first SPLASH_MS of the grace', () => {
		expect(splashStillRelevant(NOW + GRACE_MS, NOW)).toBe(true);
		expect(splashStillRelevant(NOW + GRACE_MS - SPLASH_MS + 1, NOW)).toBe(true);
	});

	it('skips it later in the grace, after it (time-up) and with no deadline', () => {
		expect(splashStillRelevant(NOW + GRACE_MS - SPLASH_MS, NOW)).toBe(false);
		expect(splashStillRelevant(NOW + 1000, NOW)).toBe(false);
		expect(splashStillRelevant(NOW - 10, NOW)).toBe(false);
		expect(splashStillRelevant(null, NOW)).toBe(false);
	});
});
