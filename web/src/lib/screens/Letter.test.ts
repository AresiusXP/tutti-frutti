// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import type { Player, View } from '$lib/protocol';
import Letter from './Letter.svelte';

interface Setup {
	youId?: string;
	chooserId?: string;
	rounds?: number;
	usedLetters?: string[];
	participants?: string[];
	players?: Player[];
}

function letterView(s: Setup = {}): View {
	const players = s.players ?? [
		player('p1', 'Ana'),
		player('p2', 'Beto', { avatar: { emoji: '🍌', color: 'banana' } }),
		player('p3', 'Caro')
	];
	return makeView({
		phase: 'letter',
		players,
		youId: s.youId ?? 'p1',
		hostId: 'p1',
		settings: makeSettings({ rounds: s.rounds ?? 5 }),
		roundNum: 3,
		usedLetters: s.usedLetters ?? ['A', 'M'],
		chooserId: s.chooserId ?? 'p1',
		round: makeRound({ number: 3, participants: s.participants ?? ['p1', 'p2', 'p3'] })
	});
}

function setup(s: Setup = {}) {
	const session = new FakeSession(letterView(s));
	return { session, ...render(Letter, { session }) };
}

const sent = (session: FakeSession) => session.sent.filter((c) => c.cmd !== 'destroy');
const stopButton = () =>
	screen.queryByRole('button', { name: /stop/i }) as HTMLButtonElement | null;

beforeEach(() => i18n.set('es'));
afterEach(() => vi.useRealTimers());

describe('Letter: the chooser', () => {
	it('sees a huge STOP button and the hint', () => {
		setup();
		expect(screen.getByText('Tocá STOP cuando quieras')).toBeTruthy();
		const button = stopButton()!;
		expect(button.textContent?.trim()).toBe('¡STOP!');
		expect(button.disabled).toBe(false);
		expect(screen.getByText('Ronda 3 de 5')).toBeTruthy();
	});

	it('does not know the letter: the "?" is decorative', () => {
		const { container } = setup({ usedLetters: [] });
		expect(container.querySelector('[aria-hidden="true"]')?.textContent).toContain('?');
		expect(screen.queryByText(/^[A-ZÑ]$/)).toBeNull();
	});

	it('sends stopLetter once, however many times it is tapped', async () => {
		const { session } = setup();
		const button = stopButton()!;
		await fireEvent.click(button);
		await fireEvent.click(button);
		await fireEvent.click(button);
		expect(sent(session)).toEqual([{ cmd: 'stopLetter', args: [] }]);
		expect(button.disabled).toBe(true);
	});

	it('reacts on pointer-down (lower latency) and does not send again on the click that follows', async () => {
		const { session } = setup();
		const button = stopButton()!;
		await fireEvent.pointerDown(button);
		await fireEvent.pointerUp(button);
		await fireEvent.click(button);
		expect(sent(session)).toEqual([{ cmd: 'stopLetter', args: [] }]);
	});

	it('ignores the secondary mouse buttons', async () => {
		const { session } = setup();
		await fireEvent.pointerDown(stopButton()!, { button: 2 });
		expect(sent(session)).toEqual([]);
	});

	it('is re-armed if the server refuses the command', async () => {
		const { session } = setup();
		await fireEvent.click(stopButton()!);
		expect(stopButton()!.disabled).toBe(true);
		session.fail('not_your_turn', 'stop_letter');
		await tick();
		expect(stopButton()!.disabled).toBe(false);
		await fireEvent.click(stopButton()!);
		expect(sent(session)).toHaveLength(2);
	});

	it('is not re-armed by an unrelated error', async () => {
		const { session } = setup();
		await fireEvent.click(stopButton()!);
		session.fail('answer_too_long', 'set_answer');
		await tick();
		expect(stopButton()!.disabled).toBe(true);
	});

	it('shows the used letters as small chips', () => {
		setup({ usedLetters: ['A', 'M', 'R'] });
		const list = screen.getByRole('list');
		expect(
			within(list)
				.getAllByRole('listitem')
				.map((li) => li.textContent?.trim())
		).toEqual(['A', 'M', 'R']);
	});

	it('shows no chips in the first round', () => {
		setup({ usedLetters: [] });
		expect(screen.queryByRole('list')).toBeNull();
	});

	it('says "Round 3" without a limit', () => {
		setup({ rounds: 0 });
		expect(screen.getByText('Ronda 3')).toBeTruthy();
		expect(screen.queryByText(/ de /)).toBeNull();
	});
});

describe('Letter: everybody else', () => {
	it('sees who is choosing and has no STOP button', () => {
		setup({ chooserId: 'p2' });
		expect(screen.getByText('Beto está eligiendo la letra…')).toBeTruthy();
		expect(stopButton()).toBeNull();
		expect(screen.queryByRole('button')).toBeNull();
		expect(screen.queryByText('Te sumás en la próxima ronda')).toBeNull();
	});

	it('announces who is choosing politely', () => {
		setup({ chooserId: 'p2' });
		expect(screen.getByRole('status').textContent).toBe('Beto está eligiendo la letra…');
	});

	it('shows the used letters and the round', () => {
		setup({ chooserId: 'p2', usedLetters: ['A', 'M'] });
		expect(screen.getByText('Ronda 3 de 5')).toBeTruthy();
		expect(screen.getAllByRole('listitem').map((li) => li.textContent?.trim())).toEqual(['A', 'M']);
	});

	it('follows a change of chooser', async () => {
		const { session } = setup({ chooserId: 'p2' });
		session.setView(letterView({ chooserId: 'p3' }));
		await tick();
		expect(screen.getByText('Caro está eligiendo la letra…')).toBeTruthy();
	});

	it('survives a chooser that is not in the list', () => {
		setup({ chooserId: 'ghost' });
		expect(screen.getByText('Alguien está eligiendo la letra…')).toBeTruthy();
	});
});

describe('Letter: players who join mid-game', () => {
	it('see the waiting view and that they join the next round', () => {
		setup({
			players: [
				player('p1', 'Ana'),
				player('p2', 'Beto'),
				player('p3', 'Caro'),
				player('p4', 'Dani')
			],
			youId: 'p4',
			chooserId: 'p2',
			participants: ['p1', 'p2', 'p3']
		});
		expect(screen.getByText('Beto está eligiendo la letra…')).toBeTruthy();
		expect(screen.getByText('Te sumás en la próxima ronda')).toBeTruthy();
		expect(screen.queryByRole('button')).toBeNull();
	});
});

describe('Letter: English', () => {
	beforeEach(() => i18n.set('en'));

	it('the chooser sees Stop! and the English hint', () => {
		setup();
		expect(stopButton()!.textContent?.trim()).toBe('Stop!');
		expect(screen.getByText('Tap Stop whenever you want')).toBeTruthy();
		expect(screen.getByText('Round 3 of 5')).toBeTruthy();
	});

	it('the others see who is choosing, in English', () => {
		setup({ chooserId: 'p2' });
		expect(screen.getByText('Beto is choosing the letter…')).toBeTruthy();
	});

	it('has no Spanish text', () => {
		setup({ chooserId: 'p2', participants: ['p1', 'p3'] });
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});
