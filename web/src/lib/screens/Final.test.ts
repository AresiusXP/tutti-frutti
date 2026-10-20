// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { i18n } from '$lib/i18n.svelte';
import { reviewView, vote, type ReviewOptions } from '$lib/review/fixtures';
import Final from './Final.svelte';

const rejectCaro = [vote('p2', 'p3', 3), vote('p4', 'p3', 3)];
const open = (options: ReviewOptions = {}) => {
	const session = new FakeSession(
		reviewView({ phase: 'final', rejectVotes: rejectCaro, ...options })
	);
	const view = render(Final, { session });
	return { session, ...view };
};
const place = (n: number) =>
	document.querySelector<HTMLElement>(`[data-testid="podium"] > li[data-place="${n}"]`);
const names = (el: HTMLElement | null) =>
	[...(el?.querySelectorAll('ul > li') ?? [])].map((li) => li.firstChild?.textContent?.trim());

beforeEach(() => i18n.set('es'));

describe('Final podium', () => {
	it('crowns the winner and puts the top three on the podium', () => {
		open();
		expect(screen.getByText('¡Ganó Beto!')).toBeTruthy();
		expect(screen.getByRole('main', { name: 'Fin de la partida' })).toBeTruthy();
		const podium = screen.getByRole('list', { name: 'Podio' });
		expect(within(podium).getByText('🏆')).toBeTruthy();
		expect(names(place(1))).toEqual(['Beto']);
		expect(names(place(2))).toEqual(['Dani']);
		expect(names(place(3))).toEqual(['Ana']);
		expect(within(place(1)!).getByText('105 pts')).toBeTruthy();
		expect(within(place(1)!).getByText('🥇')).toBeTruthy();
		expect(within(place(2)!).getByText('🥈')).toBeTruthy();
		expect(within(place(3)!).getByText('🥉')).toBeTruthy();
	});

	it('does not rely on animation or colour: places have text', () => {
		open();
		expect(within(place(1)!).getByText('Puesto 1')).toBeTruthy();
		expect(within(place(1)!).getByText('Ganador')).toBeTruthy();
		expect(within(place(2)!).queryByText('Ganador')).toBeNull();
	});

	it('shows a tie: several winners share the first place', () => {
		open({ finalTotals: { p1: 105, p2: 105, p3: 80, p4: 75 } });
		expect(screen.getByText('¡Empate entre Ana y Beto!')).toBeTruthy();
		expect(names(place(1))).toEqual(['Ana', 'Beto']);
		expect(place(2)).toBeNull();
		expect(names(place(3))).toEqual(['Caro']);
		expect(within(place(1)!).getAllByText(/Ganador/)).toHaveLength(2);
	});

	it('lists three or more winners in the headline', () => {
		open({ finalTotals: { p1: 100, p2: 100, p3: 100, p4: 70 } });
		expect(screen.getByText('¡Empate entre Ana, Beto y Caro!')).toBeTruthy();
		expect(names(place(1))).toEqual(['Ana', 'Beto', 'Caro']);
		expect(names(place(3))).toEqual([]);
	});

	it('keeps a winner who left on the podium, greyed', () => {
		open({ left: ['p2'], winners: ['p2'], youId: 'p3' });
		expect(screen.getByText('¡Ganó Beto!')).toBeTruthy();
		expect(names(place(1))).toEqual(['Beto']);
		const ranking = screen.getByRole('list', { name: 'Clasificación final' });
		expect(within(ranking).getAllByText('Se fue')).toHaveLength(1);
	});

	it('leaves out players who left from the podium and ranks them last', () => {
		// Dani left: his vote no longer counts, so Caro's answer stays valid (+10)
		open({ left: ['p4'], winners: ['p2'] });
		expect(names(place(2))).toEqual(['Caro']);
		expect(names(place(3))).toEqual(['Ana']);
		const rows = [
			...document.querySelectorAll('ol[aria-label="Clasificación final"] > li')
		] as HTMLElement[];
		expect(rows.map((r) => r.dataset.player)).toEqual(['p2', 'p3', 'p1', 'p4']);
		expect(rows.at(-1)!.dataset.rank).toBe('');
		expect(rows.at(-1)!.className).toContain('opacity-70');
	});

	it('copes with no winners at all', () => {
		open({ left: ['p1', 'p2', 'p3', 'p4'], winners: [] });
		expect(screen.getByRole('heading', { level: 1 }).textContent).toContain('Fin de la partida');
		expect(document.querySelector('[data-testid="podium"]')).toBeNull();
	});
});

describe('Final ranking and history', () => {
	it('shows the full ranking and highlights the viewer', () => {
		open({ youId: 'p3' });
		const ranking = screen.getByRole('list', { name: 'Clasificación final' });
		const rows = [...ranking.querySelectorAll(':scope > li')] as HTMLElement[];
		expect(rows.map((r) => r.dataset.player)).toEqual(['p2', 'p4', 'p1', 'p3']);
		expect(rows.map((r) => within(r).getByTestId('total').textContent?.trim())).toEqual([
			'105',
			'90',
			'70',
			'65'
		]);
		expect(within(rows[3]).getByText('Vos')).toBeTruthy();
		expect(rows[3].className).toContain('border-banana');
		expect(within(ranking).queryByRole('button')).toBeNull();
	});

	it('has an expandable per-round history, collapsed by default', async () => {
		open();
		const toggle = screen.getByRole('button', { name: /Historial por ronda/ });
		expect(toggle.getAttribute('aria-expanded')).toBe('false');
		expect(screen.queryByText('Ronda 1 · Letra M')).toBeNull();
		await fireEvent.click(toggle);
		expect(toggle.getAttribute('aria-expanded')).toBe('true');
		for (const title of ['Ronda 1 · Letra M', 'Ronda 2 · Letra B', 'Ronda 3 · Letra F']) {
			expect(screen.getByRole('heading', { name: title })).toBeTruthy();
		}
		const third = within(document.querySelector<HTMLElement>('li[data-round="3"]')!);
		expect(
			third.getAllByRole('listitem').map((li) => li.textContent?.replace(/\s+/g, ' ').trim())
		).toEqual(['Beto 50', 'Ana 40', 'Dani 40', 'Caro 30']);
		await fireEvent.click(toggle);
		expect(screen.queryByText('Ronda 1 · Letra M')).toBeNull();
	});
});

describe('Final actions', () => {
	it('host: Revancha sends rematch', async () => {
		const { session } = open();
		await fireEvent.click(screen.getByRole('button', { name: 'Revancha' }));
		expect(session.sent).toEqual([{ cmd: 'rematch', args: [] }]);
		expect(screen.queryByText(/Esperando al anfitrión/)).toBeNull();
	});

	it('everybody: Inicio leaves the room', async () => {
		const { session } = open();
		await fireEvent.click(screen.getByRole('button', { name: 'Inicio' }));
		expect(session.sent).toEqual([{ cmd: 'leave', args: [] }]);
		expect(session.closedReason).toBe('left');
	});

	it('non-host: waits for the rematch, can still go home', async () => {
		const { session } = open({ youId: 'p2' });
		expect(screen.getByText('Esperando al anfitrión para la revancha…')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Revancha' })).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Inicio' }));
		expect(session.sent).toEqual([{ cmd: 'leave', args: [] }]);
	});
});

describe('Final support link', () => {
	const sentence = 'El juego es gratis. Si te gustó, podés ayudar a mantenerlo.';

	it('invites everybody, host or not, to support the game, below the history and outside the footer', () => {
		open();
		expect(screen.getByText(sentence)).toBeTruthy();
		const link = screen.getByRole('link', { name: /^Invitame un café/ });
		expect(link.getAttribute('target')).toBe('_blank');
		expect(link.getAttribute('rel')).toBe('external noopener noreferrer');
		expect(link.closest('footer')).toBeNull();
		expect(link.closest('section')?.getAttribute('aria-label')).toBe('Invitame un café');
	});

	it('is there for a waiting guest too, and does not replace the rematch or home buttons', () => {
		open({ youId: 'p2' });
		expect(screen.getByRole('link', { name: /^Invitame un café/ })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Inicio' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Revancha' })).toBeNull();
	});

	it.each([
		[
			'en',
			'The game is free. If you enjoyed it, you can help keep it running.',
			/^Support the game/
		],
		[
			'nl',
			'Het spel is gratis. Vond je het leuk? Help mee om het draaiende te houden.',
			/^Steun het spel/
		]
	] as const)('speaks %s and names no platform', (locale, text, name) => {
		i18n.set(locale);
		open({ lang: locale });
		expect(screen.getByText(text)).toBeTruthy();
		expect(screen.getByRole('link', { name })).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/cafecito|ko-fi/i);
	});
});

describe('Final in English', () => {
	it('contains no Spanish text', async () => {
		i18n.set('en');
		open({ lang: 'en', finalTotals: { p1: 105, p2: 105, p3: 80, p4: 75 } });
		expect(screen.getByText("It's a tie between Ana and Beto!")).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Rematch' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Home' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: /Round-by-round history/ }));
		expect(screen.getByRole('heading', { name: 'Round 3 · Letter F' })).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});

	it('a single winner and a waiting guest', () => {
		i18n.set('en');
		open({ lang: 'en', youId: 'p3' });
		expect(screen.getByText('Beto wins!')).toBeTruthy();
		expect(screen.getByText('Waiting for the host to start a rematch…')).toBeTruthy();
		expect(screen.getByText('Place 1')).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});

describe('Final in Dutch', () => {
	it('contains no Spanish text and uses the glossary', async () => {
		i18n.set('nl');
		open({ lang: 'nl', finalTotals: { p1: 105, p2: 105, p3: 80, p4: 75 } });
		expect(screen.getByText('Gelijkspel tussen Ana en Beto!')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Nog een keer' })).toBeTruthy();
		expect(screen.getByRole('list', { name: 'Podium' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: /Overzicht per ronde/ }));
		expect(screen.getByRole('heading', { name: 'Ronde 3 · Letter F' })).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñ]/i);
	});

	it('a single winner and a waiting guest', () => {
		i18n.set('nl');
		open({ lang: 'nl', youId: 'p3' });
		expect(screen.getByText('Beto wint!')).toBeTruthy();
		expect(screen.getByText('Wachten tot de host nog een keer wil spelen…')).toBeTruthy();
		expect(screen.getByText('Plaats 1')).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñ]/i);
	});
});
