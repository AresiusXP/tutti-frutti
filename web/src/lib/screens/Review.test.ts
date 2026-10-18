// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { i18n } from '$lib/i18n.svelte';
import { reviewView, vote, type ReviewOptions } from '$lib/review/fixtures';
import Review from './Review.svelte';

const COMIDA = 3;
const open = (options: ReviewOptions = {}) => {
	const session = new FakeSession(reviewView(options));
	const view = render(Review, { session });
	return { session, ...view };
};
const card = (id: string) => document.querySelector<HTMLElement>(`li[data-player="${id}"]`)!;
const pending = (voters: Array<[string, boolean]>, a = 'p1', b = 'p2') => ({
	category: COMIDA,
	playerA: a,
	playerB: b,
	proposedBy: a,
	votes: voters.map(([voter, yes]) => ({ voter, yes }))
});

beforeEach(() => i18n.set('es'));

describe('Review header and navigation', () => {
	it('shows the category carousel title, the letter and one card per participant', () => {
		open({ category: 1 });
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe('País · 2/6');
		expect(screen.getByRole('img', { name: 'Letra F' })).toBeTruthy();
		expect(document.querySelectorAll('li[data-player]')).toHaveLength(4);
		expect(screen.getByRole('main', { name: 'Revisión de la ronda 3' })).toBeTruthy();
	});

	it('follows the host live: the title changes with the view', async () => {
		const { session } = open({ youId: 'p2', category: 0 });
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe('Nombre · 1/6');
		session.setView(reviewView({ youId: 'p2', category: 4 }));
		await tick();
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe('Color · 5/6');
	});

	it('host: ◀ ▶ move the shared card one category', async () => {
		const { session } = open({ category: 2 });
		await fireEvent.click(screen.getByRole('button', { name: 'Categoría siguiente' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Categoría anterior' }));
		expect(session.sent).toEqual([
			{ cmd: 'reviewNav', args: [3] },
			{ cmd: 'reviewNav', args: [1] }
		]);
	});

	it('host: clamps at both ends (no wrap)', () => {
		const { unmount } = open({ category: 0 });
		expect(
			(screen.getByRole('button', { name: 'Categoría anterior' }) as HTMLButtonElement).disabled
		).toBe(true);
		expect(
			(screen.getByRole('button', { name: 'Categoría siguiente' }) as HTMLButtonElement).disabled
		).toBe(false);
		unmount();
		open({ category: 5 });
		expect(
			(screen.getByRole('button', { name: 'Categoría siguiente' }) as HTMLButtonElement).disabled
		).toBe(true);
	});

	it('host: the progress strip jumps to a category', async () => {
		const { session } = open({ category: 0 });
		const nav = screen.getByRole('navigation', { name: 'Categorías' });
		await fireEvent.click(within(nav).getByRole('button', { name: 'Ir a la categoría 4: Comida' }));
		expect(session.sent).toEqual([{ cmd: 'reviewNav', args: [3] }]);
		expect(within(nav).getAllByRole('button')).toHaveLength(6);
		expect(
			within(nav)
				.getByRole('button', { name: 'Ir a la categoría 1: Nombre' })
				.getAttribute('aria-current')
		).toBe('step');
	});

	it('host: jumping to the category already shown sends nothing', async () => {
		const { session } = open({ category: 0 });
		await fireEvent.click(screen.getByRole('button', { name: 'Ir a la categoría 1: Nombre' }));
		expect(session.sent).toEqual([]);
	});

	it('non-host: no navigation, no finish, a calm hint and a read-only strip', () => {
		const { session } = open({ youId: 'p2', category: 1 });
		expect(screen.queryByRole('button', { name: 'Categoría siguiente' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Terminar revisión' })).toBeNull();
		const nav = screen.getByRole('navigation', { name: 'Categorías' });
		expect(within(nav).queryAllByRole('button')).toHaveLength(0);
		expect(screen.getByText('Ana controla la revisión')).toBeTruthy();
		expect(session.sent).toEqual([]);
	});
});

describe('Review finish', () => {
	it('asks first when some category was never opened, and sends on confirm', async () => {
		const { session } = open({ category: 1 });
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar revisión' }));
		expect(session.sent).toEqual([]);
		expect(screen.getByText('Todavía no abriste todas las categorías (1/6).')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Seguir revisando' }));
		expect(screen.queryByText(/Todavía no abriste/)).toBeNull();
		expect(session.sent).toEqual([]);
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar revisión' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar igual' }));
		expect(session.sent).toEqual([{ cmd: 'finishReview', args: [] }]);
	});

	it('finishes at once after the host has seen every category', async () => {
		const { session } = open({ category: 0 });
		for (let c = 1; c < 6; c++) {
			session.setView(reviewView({ category: c }));
			await tick();
		}
		await fireEvent.click(screen.getByRole('button', { name: 'Terminar revisión' }));
		expect(session.sent).toEqual([{ cmd: 'finishReview', args: [] }]);
	});
});

describe('Review cards and points badges', () => {
	it('unique = lime +10, shared = banana +5 with how many others', () => {
		open({ category: 1 });
		const badge = (id: string) => within(card(id)).getByTestId('points-badge');
		expect(badge('p2').dataset).toMatchObject({ kind: 'unique', points: '10' });
		expect(badge('p2').className).toContain('bg-lime');
		expect(badge('p1').dataset).toMatchObject({ kind: 'shared', points: '5' });
		expect(badge('p1').className).toContain('bg-banana');
		expect(within(card('p1')).getByText('Igual a 1 más')).toBeTruthy();
		expect(within(card('p2')).queryByText(/Igual a/)).toBeNull();
	});

	it('says how many others share an answer', () => {
		open({ category: 4 });
		expect(within(card('p1')).getByText('Igual a 3 más')).toBeTruthy();
	});

	it('wrong letter = grey 0 and a chip, no reject control', () => {
		open({ category: 0 });
		const dani = within(card('p4'));
		expect(dani.getByText('No empieza con F')).toBeTruthy();
		expect(dani.getByTestId('points-badge').dataset.kind).toBe('wrong_letter');
		expect(dani.getByTestId('points-badge').className).toContain('bg-night-3');
		expect(dani.queryByRole('button')).toBeNull();
	});

	it('empty = "— sin respuesta —" in grey, no badge, no reject control', () => {
		open({ category: 2 });
		const caro = within(card('p3'));
		expect(caro.getByText('— sin respuesta —')).toBeTruthy();
		expect(caro.queryByTestId('points-badge')).toBeNull();
		expect(caro.queryByRole('button')).toBeNull();
	});

	it('rejected = struck through, grey 0 and "Rechazada"', () => {
		open({ category: COMIDA, rejectVotes: [vote('p2', 'p3', COMIDA), vote('p4', 'p3', COMIDA)] });
		const caro = within(card('p3'));
		expect(caro.getByText('Rechazada')).toBeTruthy();
		expect(caro.getByTestId('answer').className).toContain('line-through');
		expect(caro.getByTestId('points-badge').dataset.kind).toBe('rejected');
		expect(caro.getByTestId('points-badge').textContent).toContain('0');
	});

	it('badges follow the live result when the view changes', async () => {
		const { session } = open({ category: COMIDA });
		expect(within(card('p3')).getByTestId('points-badge').dataset.kind).toBe('unique');
		session.setView(
			reviewView({
				category: COMIDA,
				rejectVotes: [vote('p2', 'p3', COMIDA), vote('p4', 'p3', COMIDA)]
			})
		);
		await tick();
		expect(within(card('p3')).getByTestId('points-badge').dataset.kind).toBe('rejected');
	});

	it('marks your own card and has no controls on it', () => {
		open({ category: 1 });
		const mine = within(card('p1'));
		expect(mine.getByText('Vos')).toBeTruthy();
		expect(mine.queryByRole('button')).toBeNull();
		expect(within(card('p2')).queryByText('Vos')).toBeNull();
	});

	it('wraps long answers without overflowing', () => {
		const long = 'Fideos caseros con salsa de hongos y ajo';
		open({ category: COMIDA, answers: { p3: { [COMIDA]: long } } });
		const answer = within(card('p3')).getByTestId('answer');
		expect(answer.textContent?.trim()).toBe(long);
		expect(answer.className).toContain('break-words');
		expect(answer.className).toContain('overflow-wrap:anywhere');
	});

	it('shows offline and left players', () => {
		open({ category: 1, offline: ['p2'], left: ['p4'] });
		expect(within(card('p2')).getByText('Sin conexión')).toBeTruthy();
		expect(within(card('p4')).getByText('Se fue')).toBeTruthy();
	});
});

describe('Review reject votes', () => {
	it('votes with exactly (category, target) and shows the count against the votes needed', async () => {
		const { session } = open({ category: COMIDA, rejectVotes: [vote('p4', 'p3', COMIDA)] });
		const button = within(card('p3')).getByRole('button', {
			name: 'Rechazar la respuesta de Caro: 1 de 2 votos'
		});
		expect(button.getAttribute('aria-pressed')).toBe('false');
		expect(button.textContent).toContain('1/2');
		await fireEvent.click(button);
		expect(session.sent).toEqual([{ cmd: 'voteReject', args: [COMIDA, 'p3'] }]);
	});

	it('tapping again withdraws the vote', async () => {
		const { session } = open({ category: COMIDA, rejectVotes: [vote('p1', 'p2', COMIDA)] });
		const button = within(card('p2')).getByRole('button', {
			name: /Rechazar la respuesta de Beto/
		});
		expect(button.getAttribute('aria-pressed')).toBe('true');
		await fireEvent.click(button);
		expect(session.sent).toEqual([{ cmd: 'voteReject', args: [COMIDA, 'p2', true] }]);
	});

	it('needs more than half of the OTHER connected participants', () => {
		open({ category: COMIDA, offline: ['p4'] });
		// p1 p2 p3 connected, p3 is the author: 2 eligible, 2 needed
		expect(within(card('p3')).getByRole('button', { name: /Caro: 0 de 2 votos/ })).toBeTruthy();
		// a connected author does not count himself: p2 has p1 and p3 to convince
		expect(within(card('p2')).getByRole('button', { name: /Beto: 0 de 2 votos/ })).toBeTruthy();
	});

	it('four connected players need 2 of the 3 others', () => {
		open({ category: COMIDA });
		expect(within(card('p3')).getByRole('button', { name: /0 de 2 votos/ })).toBeTruthy();
	});

	it('shows who voted with avatars and an accessible label', () => {
		open({ category: COMIDA, rejectVotes: [vote('p2', 'p3', COMIDA), vote('p4', 'p3', COMIDA)] });
		expect(
			within(card('p3')).getByRole('img', { name: 'Votaron en contra: Beto, Dani' })
		).toBeTruthy();
		expect(within(card('p2')).queryByRole('img')).toBeNull();
	});

	it('a rejected answer keeps its button so a vote can be withdrawn', async () => {
		const { session } = open({
			category: COMIDA,
			rejectVotes: [vote('p1', 'p3', COMIDA), vote('p2', 'p3', COMIDA)]
		});
		const button = within(card('p3')).getByRole('button', { name: /Caro: 2 de 2 votos/ });
		await fireEvent.click(button);
		expect(session.sent).toEqual([{ cmd: 'voteReject', args: [COMIDA, 'p3', true] }]);
	});

	it('spectators and non-participants cannot vote or propose', () => {
		open({ spectator: true, category: 1 });
		expect(screen.getByText('Estás mirando esta ronda. Te sumás en la próxima.')).toBeTruthy();
		expect(screen.queryByRole('button', { name: /Rechazar/ })).toBeNull();
		expect(screen.queryByRole('button', { name: /Son iguales/ })).toBeNull();
	});
});

describe('Review "Son iguales"', () => {
	it('picks two answers and proposes exactly (category, first, second)', async () => {
		const { session } = open({ category: COMIDA });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		expect(screen.getByTestId('merge-banner').textContent).toContain('Elegí la primera respuesta');
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Ana' }));
		expect(screen.getByTestId('merge-banner').textContent).toContain(
			'Elegí la que es igual a «Frutilla»'
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Beto' }));
		expect(session.sent).toEqual([{ cmd: 'proposeMerge', args: [COMIDA, 'p1', 'p2'] }]);
		expect(screen.queryByTestId('merge-banner')).toBeNull();
	});

	it('can undo the first pick and cancel', async () => {
		const { session } = open({ category: COMIDA });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Beto' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Quitar la elección de Beto' }));
		expect(screen.getByTestId('merge-banner').textContent).toContain('Elegí la primera respuesta');
		await fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }));
		expect(screen.queryByTestId('merge-banner')).toBeNull();
		expect(session.sent).toEqual([]);
	});

	it('never offers answers that cannot be merged', async () => {
		open({ category: 0 });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		// Nombre: Fabián, Fabián (already the same), Federico, Pedro (wrong letter)
		expect(screen.queryByRole('button', { name: 'Elegir la respuesta de Dani' })).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Ana' }));
		expect(screen.queryByRole('button', { name: 'Elegir la respuesta de Beto' })).toBeNull();
		expect(within(card('p2')).getByText('Ya cuentan como iguales')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Elegir la respuesta de Caro' })).toBeTruthy();
	});

	it('does not offer a pair that already has an open proposal', async () => {
		open({ category: COMIDA, proposals: [pending([['p1', true]])] });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Ana' }));
		expect(within(card('p2')).getByText('Ya hay una propuesta')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Elegir la respuesta de Beto' })).toBeNull();
	});

	it('hides the control when nothing can be merged', () => {
		open({ category: 4 });
		expect(screen.queryByRole('button', { name: /Son iguales/ })).toBeNull();
	});

	it('leaves the selection when the category changes', async () => {
		const { session } = open({ category: COMIDA });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		expect(screen.getByTestId('merge-banner')).toBeTruthy();
		session.setView(reviewView({ category: 1 }));
		await tick();
		expect(screen.queryByTestId('merge-banner')).toBeNull();
	});

	it('drops the selection when the first answer stops being mergeable', async () => {
		const { session } = open({ category: COMIDA });
		await fireEvent.click(screen.getByRole('button', { name: /Son iguales/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Elegir la respuesta de Dani' }));
		session.setView(
			reviewView({
				category: COMIDA,
				answers: { p1: { [COMIDA]: '' }, p2: { [COMIDA]: '' }, p3: { [COMIDA]: '' } }
			})
		);
		await tick();
		expect(screen.queryByTestId('merge-banner')).toBeNull();
	});
});

describe('Review merge proposals', () => {
	it('shows the proposal with its tally and lets you vote Sí / No', async () => {
		const { session } = open({
			youId: 'p3',
			category: COMIDA,
			proposals: [
				pending([
					['p1', true],
					['p4', false]
				])
			]
		});
		const proposal = within(document.querySelector<HTMLElement>('li[data-proposal="1"]')!);
		expect(proposal.getByText('Ana y Beto: ¿son iguales?')).toBeTruthy();
		expect(proposal.getByText('«Frutilla» y «Frutilas»')).toBeTruthy();
		expect(proposal.getByTestId('tally').textContent?.trim()).toBe('1 de 4 dicen que sí');
		expect(proposal.getByText('Todavía no alcanza')).toBeTruthy();
		const yes = proposal.getByRole('button', { name: 'Sí, Ana y Beto son iguales' });
		const no = proposal.getByRole('button', { name: 'No, Ana y Beto no son iguales' });
		expect(yes.getAttribute('aria-pressed')).toBe('false');
		expect(no.getAttribute('aria-pressed')).toBe('false');
		await fireEvent.click(yes);
		await fireEvent.click(no);
		expect(session.sent).toEqual([
			{ cmd: 'voteMerge', args: [1, true] },
			{ cmd: 'voteMerge', args: [1, false] }
		]);
	});

	it('shows your own vote', () => {
		open({ category: COMIDA, proposals: [pending([['p1', true]])] });
		const proposal = within(document.querySelector<HTMLElement>('li[data-proposal="1"]')!);
		expect(proposal.getByRole('button', { name: /^Sí/ }).getAttribute('aria-pressed')).toBe('true');
		expect(proposal.getByRole('button', { name: /^No/ }).getAttribute('aria-pressed')).toBe(
			'false'
		);
	});

	it('says when the proposal passes and the answers count as the same', () => {
		open({
			youId: 'p4',
			category: COMIDA,
			proposals: [
				pending([
					['p1', true],
					['p2', true],
					['p3', true]
				])
			]
		});
		const proposal = within(document.querySelector<HTMLElement>('li[data-proposal="1"]')!);
		expect(proposal.getByTestId('tally').textContent?.trim()).toBe('3 de 4 dicen que sí');
		expect(proposal.getByText('Cuentan como iguales')).toBeTruthy();
		expect(within(card('p1')).getByTestId('points-badge').dataset.kind).toBe('shared');
	});

	it('only lists the proposals of the category on screen', () => {
		open({ category: 1, proposals: [pending([['p1', true]])] });
		expect(document.querySelector('li[data-proposal]')).toBeNull();
	});

	it('spectators see the tally but no vote buttons', () => {
		open({ spectator: true, category: COMIDA, proposals: [pending([['p1', true]])] });
		const proposal = within(document.querySelector<HTMLElement>('li[data-proposal="1"]')!);
		expect(proposal.getByTestId('tally')).toBeTruthy();
		expect(proposal.queryByRole('button')).toBeNull();
	});
});

describe('Review in English', () => {
	it('contains no Spanish text', async () => {
		i18n.set('en');
		const { session } = open({
			lang: 'en',
			category: COMIDA,
			rejectVotes: [vote('p2', 'p3', COMIDA), vote('p4', 'p3', COMIDA)],
			proposals: [pending([['p1', true]])]
		});
		await fireEvent.click(screen.getByRole('button', { name: /Same/ }));
		expect(screen.getByRole('button', { name: 'Finish review' })).toBeTruthy();
		expect(screen.getByRole('heading', { level: 1 }).textContent?.trim()).toBe('Food · 4/6');
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
		expect(document.body.textContent).toContain('Pick the first answer');
		session.setView(reviewView({ lang: 'en', youId: 'p2', category: 2 }));
		await tick();
		expect(document.body.textContent).toContain('Ana is running the review');
		expect(document.body.textContent).toContain('— no answer —');
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});
