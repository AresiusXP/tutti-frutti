// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fieldProblems, labelNameMismatches } from '#lib/a11y.testkit.ts';
import { i18n } from '#lib/i18n.svelte.ts';
import HelpSheet from './HelpSheet.svelte';
import type { HelpSection } from './sections.ts';

function open(section?: HelpSection) {
	const onclose = vi.fn();
	render(HelpSheet, { section, onclose });
	return { onclose };
}

const header = (name: RegExp | string) => screen.getByRole('button', { name });
const expanded = () =>
	screen
		.queryAllByRole('button', { expanded: true })
		.map((b) => b.getAttribute('id')?.replace('help-heading-', ''));

beforeEach(() => i18n.set('es'));
afterEach(() => cleanup());

describe('HelpSheet', () => {
	it('is a dialog titled "How to play" with the four parts of the guide', () => {
		open();
		const dialog = screen.getByRole('dialog', { name: 'Cómo se juega' });
		const parts = within(dialog).getAllByRole('heading', { level: 3 });
		expect(parts.map((h) => h.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
			'🎯 Lo básico ▾',
			'✍️ Durante la ronda ▾',
			'👀 Revisar las respuestas ▾',
			'🏆 Puntos y ganador ▾'
		]);
	});

	it.each(['basics', 'playing', 'review', 'points'] as const)(
		'starts with the %s part open and the others closed',
		(section) => {
			open(section);
			expect(expanded()).toEqual([section]);
		}
	);

	it('starts with the basics when no part is asked for', () => {
		open();
		expect(expanded()).toEqual(['basics']);
	});

	it('opens one part at a time and closes the open one when it is tapped again', async () => {
		open('basics');
		await fireEvent.click(header(/Revisar las respuestas/));
		expect(expanded()).toEqual(['review']);
		expect(document.getElementById('help-panel-review')!.hidden).toBe(false);
		expect(document.getElementById('help-panel-basics')!.hidden).toBe(true);
		await fireEvent.click(header(/Revisar las respuestas/));
		expect(expanded()).toEqual([]);
	});

	it('explains Reject and Same on the review part, with the real wording of the buttons', () => {
		open('review');
		const panel = document.getElementById('help-panel-review')!;
		expect(panel.textContent).toContain('Rechazar:');
		expect(panel.textContent).toContain('Son iguales:');
		expect(panel.textContent).toContain('Preguntar a la IA');
	});

	it('lists the points: 10 unique, 5 shared, 0 for the rest', async () => {
		open('points');
		const panel = document.getElementById('help-panel-points')!;
		const rows = within(panel).getAllByRole('listitem');
		expect(rows.map((r) => r.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
			'+10 10 puntos Una respuesta válida que nadie más tiene',
			'+5 5 puntos Una respuesta válida que otro jugador también tiene',
			'0 0 puntos Vacía, con otra letra o rechazada'
		]);
	});

	it('closes with the footer button', async () => {
		const { onclose } = open();
		await fireEvent.click(screen.getByRole('button', { name: 'Entendido' }));
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('every control is 48 px and named by what it shows', () => {
		open();
		const dialog = screen.getByRole('dialog');
		expect(labelNameMismatches(dialog)).toEqual([]);
		expect(fieldProblems(dialog)).toEqual([]);
		for (const id of ['basics', 'playing', 'review', 'points']) {
			expect(document.getElementById(`help-heading-${id}`)!.className).toContain('min-h-14');
		}
	});

	it('speaks each language: Stop! in English and Dutch, ¡BASTA! in Spanish, no Spanish outside it', async () => {
		const text = () => document.querySelector('[data-testid="help-sheet"]')!.textContent!;
		const everything = async () => {
			for (const id of ['basics', 'playing', 'review', 'points']) {
				const button = document.getElementById(`help-heading-${id}`)!;
				if (button.getAttribute('aria-expanded') === 'false') await fireEvent.click(button);
			}
		};
		open('playing');
		await everything();
		expect(text()).toContain('¡BASTA!');
		cleanup();

		for (const [locale, title] of [
			['en', 'How to play'],
			['nl', 'Zo speel je het']
		] as const) {
			i18n.set(locale);
			open('playing');
			expect(screen.getByRole('dialog', { name: title })).toBeTruthy();
			await everything();
			expect(text(), locale).toContain('Stop!');
			expect(text(), locale).not.toMatch(/[¡¿ñ]|basta/i);
			cleanup();
		}
	});
});
