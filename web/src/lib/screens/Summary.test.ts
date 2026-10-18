// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { i18n } from '$lib/i18n.svelte';
import { reviewView, vote, type ReviewOptions } from '$lib/review/fixtures';
import Summary from './Summary.svelte';

const rejectCaro = [vote('p2', 'p3', 3), vote('p4', 'p3', 3)];
const open = (options: ReviewOptions = {}) => {
	const session = new FakeSession(
		reviewView({ phase: 'round_summary', rejectVotes: rejectCaro, ...options })
	);
	const view = render(Summary, { session });
	return { session, ...view };
};
const row = (id: string) => document.querySelector<HTMLElement>(`li[data-player="${id}"]`)!;

beforeEach(() => i18n.set('es'));

describe('Summary ranking', () => {
	it('titles the round with its number and letter', () => {
		open();
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe('Ronda 3 · Letra F');
		expect(screen.getByRole('main', { name: 'Resumen de la ronda 3' })).toBeTruthy();
	});

	it('ranks players by running total with their points this round', () => {
		open();
		const list = screen.getByRole('list', { name: 'Clasificación' });
		const rows = [...list.querySelectorAll(':scope > li')] as HTMLElement[];
		expect(rows.map((r) => r.dataset.player)).toEqual(['p2', 'p4', 'p1', 'p3']);
		expect(rows.map((r) => r.dataset.rank)).toEqual(['1', '2', '3', '4']);
		const totals = rows.map((r) => within(r).getByTestId('total').textContent?.trim());
		expect(totals).toEqual(['105', '90', '70', '65']);
		const deltas = rows.map((r) =>
			within(r).getByTestId('delta').textContent?.replace(/\s+/g, ' ').trim()
		);
		expect(deltas).toEqual([
			'+50 50 puntos esta ronda',
			'+40 40 puntos esta ronda',
			'+40 40 puntos esta ronda',
			'+30 30 puntos esta ronda'
		]);
	});

	it('players with the same total share the rank', () => {
		const merge = {
			category: 3,
			playerA: 'p1',
			playerB: 'p2',
			proposedBy: 'p1',
			votes: ['p1', 'p2', 'p4'].map((voter) => ({ voter, yes: true }))
		};
		open({ proposals: [merge] });
		// the merge costs Ana and Beto 5 points: Ana 65 = Caro 65
		expect(row('p1').dataset.rank).toBe('3');
		expect(row('p3').dataset.rank).toBe('3');
		expect(within(row('p1')).getByTestId('total').textContent?.trim()).toBe('65');
	});

	it('highlights the viewer row', () => {
		open({ youId: 'p3' });
		expect(within(row('p3')).getByText('Vos')).toBeTruthy();
		expect(row('p3').className).toContain('border-banana');
		expect(within(row('p1')).queryByText('Vos')).toBeNull();
		expect(row('p1').className).not.toContain('border-banana');
	});

	it('shows places gained or lost since the previous round', () => {
		open();
		const change = (id: string) =>
			within(row(id)).queryByTestId('change')?.textContent?.replace(/\s+/g, ' ').trim();
		expect(change('p1')).toBe('▲1 Subió 1');
		expect(change('p3')).toBe('▼1 Bajó 1');
		expect(change('p2')).toBeUndefined();
	});

	it('shows no arrows in the first round', () => {
		const session = new FakeSession(
			reviewView({ phase: 'round_summary', rejectVotes: rejectCaro })
		);
		session.setView({ ...session.view!, history: session.view!.history.slice(-1) });
		render(Summary, { session });
		expect(document.querySelectorAll('[data-testid="change"]')).toHaveLength(0);
	});

	it('keeps the per-category breakdown collapsed until asked', async () => {
		open();
		const toggle = within(row('p1')).getByRole('button');
		expect(toggle.getAttribute('aria-expanded')).toBe('false');
		expect(within(row('p1')).queryByText('Frutilla')).toBeNull();
		await fireEvent.click(toggle);
		expect(toggle.getAttribute('aria-expanded')).toBe('true');
		const details = within(row('p1'));
		expect(details.getByText('Comida')).toBeTruthy();
		expect(details.getByText('Frutilla')).toBeTruthy();
		expect(details.getAllByTestId('points-badge')).toHaveLength(6);
		await fireEvent.click(toggle);
		expect(within(row('p1')).queryByText('Frutilla')).toBeNull();
	});

	it('breakdown marks rejected and wrong-letter answers', async () => {
		open();
		await fireEvent.click(within(row('p3')).getByRole('button'));
		const caro = within(row('p3'));
		expect(caro.getByText('Fierro').className).toContain('line-through');
		expect(caro.getByText('Rechazada')).toBeTruthy();
		expect(caro.getByText('— sin respuesta —')).toBeTruthy();
		await fireEvent.click(within(row('p4')).getByRole('button'));
		expect(within(row('p4')).getByText('No empieza con F')).toBeTruthy();
	});

	it('players who joined mid-game and players who left are shown without a breakdown', () => {
		open({ spectator: true, left: ['p4'] });
		expect(within(row('p5')).getByText('Se suma en la próxima ronda')).toBeTruthy();
		expect(within(row('p5')).queryByRole('button')).toBeNull();
		expect(within(row('p4')).getByText('Se fue')).toBeTruthy();
		expect(row('p4').dataset.rank).toBe('');
	});
});

describe('Summary actions', () => {
	it('host: "Siguiente ronda" sends nextRound', async () => {
		const { session } = open();
		expect(screen.queryByTestId('last-round')).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Siguiente ronda' }));
		expect(session.sent).toEqual([{ cmd: 'nextRound', args: [] }]);
	});

	it('host: "Terminar partida" asks first, then sends endGame', async () => {
		const { session } = open();
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar partida' }));
		expect(session.sent).toEqual([]);
		expect(screen.getByText('¿Terminar la partida ahora?')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Seguir jugando' }));
		expect(screen.queryByText('¿Terminar la partida ahora?')).toBeNull();
		expect(session.sent).toEqual([]);
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar partida' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, terminar' }));
		expect(session.sent).toEqual([{ cmd: 'endGame', args: [] }]);
	});

	it('non-host: waits for the host and has no buttons to continue', () => {
		const { session } = open({ youId: 'p2' });
		expect(screen.getByText('Esperando al anfitrión…')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Siguiente ronda' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Terminar partida' })).toBeNull();
		expect(session.sent).toEqual([]);
	});

	it('last round (round limit): says so and makes "Terminar partida" the only action', async () => {
		const { session } = open({ rounds: 3 });
		const note = screen.getByTestId('last-round');
		expect(within(note).getByText('Última ronda')).toBeTruthy();
		expect(within(note).getByText('Ya se jugaron todas las rondas.')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Siguiente ronda' })).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar partida' }));
		expect(session.sent).toEqual([{ cmd: 'endGame', args: [] }]);
	});

	it('last round (no letters left): explains why', () => {
		const session = new FakeSession(reviewView({ phase: 'round_summary', rounds: 0 }));
		const view = session.view!;
		session.setView({
			...view,
			usedLetters: [
				'A',
				'B',
				'C',
				'D',
				'E',
				'F',
				'G',
				'H',
				'I',
				'J',
				'L',
				'M',
				'N',
				'O',
				'P',
				'R',
				'S',
				'T',
				'U',
				'V'
			]
		});
		render(Summary, { session });
		expect(screen.getByText('No quedan letras por jugar.')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Siguiente ronda' })).toBeNull();
	});

	it('guests of the last round just wait', () => {
		open({ rounds: 3, youId: 'p4' });
		expect(screen.getByText('Última ronda')).toBeTruthy();
		expect(screen.getByText('Esperando al anfitrión…')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Terminar partida' })).toBeNull();
	});

	it('follows a new host when the host changes', async () => {
		const { session } = open({ youId: 'p2' });
		expect(screen.queryByRole('button', { name: 'Siguiente ronda' })).toBeNull();
		session.setView(
			reviewView({ phase: 'round_summary', youId: 'p2', hostId: 'p2', rejectVotes: rejectCaro })
		);
		await tick();
		expect(screen.getByRole('button', { name: 'Siguiente ronda' })).toBeTruthy();
	});
});

describe('Summary in English', () => {
	it('contains no Spanish text', async () => {
		i18n.set('en');
		open({ lang: 'en', rounds: 3 });
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe(
			'Round 3 · Letter F'
		);
		expect(screen.getByText('Last round')).toBeTruthy();
		await fireEvent.click(within(row('p3')).getByRole('button'));
		expect(screen.getByRole('button', { name: 'End game' })).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});

	it('guest sees the waiting message in English', () => {
		i18n.set('en');
		open({ lang: 'en', youId: 'p2' });
		expect(screen.getByText('Waiting for the host…')).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});
