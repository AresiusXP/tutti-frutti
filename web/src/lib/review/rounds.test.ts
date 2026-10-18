import { describe, expect, it } from 'vitest';
import { player } from '$lib/game/fixtures';
import { availableLetters, lastRoundReason } from './rounds';
import { avatarOf, joinNames, playerName } from './players';

describe('availableLetters', () => {
	it('Spanish has 27 letters with Ñ after N', () => {
		const all = availableLetters('es', false, []);
		expect(all).toHaveLength(27);
		expect(all.indexOf('Ñ')).toBe(all.indexOf('N') + 1);
	});

	it('English has 26 letters', () => {
		expect(availableLetters('en', false, [])).toHaveLength(26);
		expect(availableLetters('en', false, [])).not.toContain('Ñ');
	});

	it('removes the hard letters of each language when asked', () => {
		const es = availableLetters('es', true, []);
		expect(es).toHaveLength(20);
		for (const hard of ['K', 'Ñ', 'Q', 'W', 'X', 'Y', 'Z']) expect(es).not.toContain(hard);
		const en = availableLetters('en', true, []);
		expect(en).toHaveLength(20);
		for (const hard of ['Q', 'U', 'V', 'X', 'Y', 'Z']) expect(en).not.toContain(hard);
	});

	it('removes the used letters', () => {
		expect(availableLetters('es', true, ['A', 'B'])).toHaveLength(18);
	});

	it('is empty for an unknown language', () => {
		// @ts-expect-error not a supported language
		expect(availableLetters('fr', false, [])).toEqual([]);
	});
});

describe('lastRoundReason', () => {
	const settings = (rounds: number, lang: 'es' | 'en' = 'es', excludeHardLetters = true) => ({
		rounds,
		lang,
		excludeHardLetters
	});

	it('the round limit ends the game', () => {
		expect(lastRoundReason(settings(5), 5, ['A', 'B', 'C', 'D', 'E'])).toBe('rounds');
		expect(lastRoundReason(settings(5), 6, [])).toBe('rounds');
	});

	it('the game goes on before the limit', () => {
		expect(lastRoundReason(settings(5), 4, ['A', 'B', 'C', 'D'])).toBeNull();
	});

	it('unlimited rounds go on while letters remain', () => {
		expect(lastRoundReason(settings(0), 30, ['A'])).toBeNull();
	});

	it('runs out of letters', () => {
		const every = availableLetters('en', true, []);
		expect(lastRoundReason(settings(0, 'en'), 20, every)).toBe('letters');
		expect(lastRoundReason(settings(0, 'en'), 19, every.slice(1))).toBeNull();
	});

	it('hard letters count only when they are excluded', () => {
		const easy = availableLetters('es', true, []);
		expect(lastRoundReason(settings(0, 'es', true), 20, easy)).toBe('letters');
		expect(lastRoundReason(settings(0, 'es', false), 20, easy)).toBeNull();
	});

	it('the round limit wins when both apply', () => {
		const every = availableLetters('es', true, []);
		expect(lastRoundReason(settings(20), 20, every)).toBe('rounds');
	});
});

describe('players helpers', () => {
	it('avatarOf passes the avatar through', () => {
		const ana = player('p1', 'Ana', { avatar: { emoji: '🍌', color: 'banana' } });
		expect(avatarOf(ana)).toEqual({ emoji: '🍌', color: 'banana' });
	});

	it('playerName falls back to the id', () => {
		const players = [player('p1', 'Ana')];
		expect(playerName(players, 'p1')).toBe('Ana');
		expect(playerName(players, 'ghost')).toBe('ghost');
	});

	it('joinNames follows the language', () => {
		expect(joinNames(['Ana', 'Beto', 'Caro'], 'es')).toBe('Ana, Beto y Caro');
		expect(joinNames(['Ana', 'Beto', 'Caro'], 'en')).toBe('Ana, Beto, and Caro');
		expect(joinNames(['Ana', 'Beto'], 'en')).toBe('Ana and Beto');
		expect(joinNames(['Ana'], 'es')).toBe('Ana');
	});

	it('joinNames survives an unknown locale', () => {
		expect(joinNames(['Ana', 'Beto'], 'not a locale!!')).toBe('Ana, Beto');
	});
});
