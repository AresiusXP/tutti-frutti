import { describe, expect, it } from 'vitest';
import { accountErrorMessage } from './errors';
import { averagePoints, winRate } from './stats';
import { STATS } from './account.testkit';
import { i18n } from '$lib/i18n.svelte';

describe('derived stats', () => {
	it('win rate is a rounded whole percentage', () => {
		expect(winRate(STATS)).toBe(42); // 5 / 12
		expect(winRate({ ...STATS, gamesPlayed: 3, wins: 2 })).toBe(67);
		expect(winRate({ ...STATS, gamesPlayed: 4, wins: 4 })).toBe(100);
		expect(winRate({ ...STATS, wins: 0 })).toBe(0);
	});

	it('average points has one decimal', () => {
		expect(averagePoints(STATS)).toBe(103.3); // 1240 / 12
		expect(averagePoints({ ...STATS, gamesPlayed: 4, totalPoints: 10 })).toBe(2.5);
		expect(averagePoints({ ...STATS, gamesPlayed: 3, totalPoints: 100 })).toBe(33.3);
	});

	it('never divides by zero before the first game', () => {
		const none = { gamesPlayed: 0, wins: 0, totalPoints: 0, bestRound: 0, uniqueAnswers: 0 };
		expect(winRate(none)).toBeNull();
		expect(averagePoints(none)).toBeNull();
	});
});

describe('accountErrorMessage', () => {
	it('explains each REST failure in both languages and falls back to a generic sentence', () => {
		const texts = (locale: 'es' | 'en') => {
			i18n.set(locale);
			return ['unauthorized', 'rate_limited', 'network', 'server_busy', 'whatever'].map((code) =>
				accountErrorMessage(code)
			);
		};
		const es = texts('es');
		const en = texts('en');
		i18n.set('es');
		expect(es).toEqual([
			'Tu sesión venció. Iniciá sesión de nuevo.',
			'Vas muy rápido. Esperá un momento y probá de nuevo.',
			'No hay conexión con el servidor. Revisá tu internet y probá de nuevo.',
			'El servidor está ocupado. Probá de nuevo en un rato.',
			'Algo salió mal. Probá de nuevo.'
		]);
		expect(new Set(en).size).toBe(5);
		for (const text of en) expect(text).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});
