// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fieldProblems, headingOnes, labelNameMismatches } from '$lib/a11y.testkit';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import { reviewView, type ReviewOptions } from '$lib/review/fixtures';
import RoomScreen from '$lib/screens/RoomScreen.svelte';
import type { View } from '$lib/protocol';
import { toasts } from '$lib/toast.svelte';
import { safetyApi } from './api';

/**
 * Where the report / block affordances live: every place that shows another player, a custom category
 * label or the AI's note, never for oneself or an automated seat, and never for an answer that is hidden.
 */
const open = (view: View, setup?: (s: FakeSession) => void) => {
	const session = new FakeSession(view);
	setup?.(session);
	render(RoomScreen, { session });
	return session;
};

/** Gives seat `id` the id of an automated seat everywhere in the (serialised) view. */
const asBot = (view: View, id: string): View =>
	JSON.parse(JSON.stringify(view).replaceAll(`"${id}"`, `"bot:${id}"`)) as View;

const flags = () =>
	screen
		.queryAllByTestId('safety-button')
		.map((b) => b.getAttribute('aria-label') ?? b.textContent?.trim());

beforeEach(() => {
	i18n.set('en');
	toasts.items = [];
	vi.restoreAllMocks();
});
afterEach(() => cleanup());

describe('lobby', () => {
	const players = [player('p1', 'Ana'), player('p2', 'Beto'), player('p3', 'Caro')];

	it('every other player has a flag; oneself does not; an automated seat does not', () => {
		open(asBot(makeView({ players }), 'p3'));
		expect(flags()).toEqual(['Report or block Beto']);
	});

	it('opening it shows the player menu (report and block) as one dialog', async () => {
		open(makeView({ players }));
		await fireEvent.click(screen.getByRole('button', { name: 'Report or block Caro' }));
		const dialog = await screen.findByRole('dialog', { name: 'Options for Caro' });
		expect(within(dialog).getByRole('button', { name: 'Report nickname or avatar' })).toBeTruthy();
		expect(within(dialog).getByRole('button', { name: 'Block and leave' })).toBeTruthy();
		expect(within(dialog).queryByRole('button', { name: 'Report this answer' })).toBeNull();
	});

	it('the flag keeps a 48 px hit area', () => {
		open(makeView({ players }));
		for (const flag of screen.getAllByTestId('safety-button'))
			expect(flag.className).toContain('size-12');
	});

	it('a guest can report a custom category label; the host (who wrote it) is not offered it', () => {
		const settings = makeSettings({
			categories: [{ id: 'nombre' }, { custom: 'Superhéroe' }, { id: 'pais' }]
		});
		const guest = open(makeView({ players, settings, youId: 'p2' }));
		expect(guest.view?.hostId).toBe('p1');
		expect(flags()).toContain('Report the category Superhéroe');
		expect(
			screen
				.getAllByTestId('safety-button')
				.find((b) => b.getAttribute('aria-label') === 'Report the category Superhéroe')!.className
		).toContain('size-12');
		cleanup();
		open(makeView({ players, settings }));
		expect(flags()).not.toContain('Report the category Superhéroe');
	});

	it('reports the label with the category index and the label as the guest sees it', async () => {
		const report = vi.spyOn(safetyApi, 'report').mockResolvedValue({ id: 'r1' });
		const settings = makeSettings({
			categories: [{ id: 'nombre' }, { custom: 'Superhéroe' }, { id: 'pais' }]
		});
		const session = open(makeView({ players, settings, youId: 'p2' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Report the category Superhéroe' }));
		await fireEvent.click(await screen.findByRole('radio', { name: 'Harassment or insults' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(report).toHaveBeenCalledTimes(1));
		expect(report.mock.calls[0][0]).toMatchObject({
			kind: 'custom_label',
			code: session.code,
			roomInstanceId: 'room-instance-1',
			ref: { category: 1, label: 'Superhéroe' }
		});
		await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
		expect(toasts.items.map((t) => t.text)).toEqual(['Report sent. Thank you!']);
	});
});

describe('letter and playing (no player rows)', () => {
	const players = [player('p1', 'Ana'), player('p2', 'Beto'), player('bot:x', 'Robin')];

	it('the letter screen offers "Report or block a player" as a visible 48 px button', () => {
		open(
			makeView({
				phase: 'letter',
				players,
				chooserId: 'p2',
				roundNum: 1,
				round: makeRound({ participants: ['p1', 'p2', 'bot:x'] })
			})
		);
		const button = screen.getByRole('button', { name: 'Report or block a player' });
		expect(button.className).toContain('h-12');
		expect(button.textContent).toContain('Report or block a player');
	});

	it('the players list excludes oneself and the automated seat', async () => {
		open(
			makeView({
				phase: 'letter',
				players,
				chooserId: 'p2',
				roundNum: 1,
				round: makeRound({ participants: ['p1', 'p2', 'bot:x'] })
			})
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Report or block a player' }));
		const dialog = await screen.findByRole('dialog', { name: 'Report or block' });
		expect(
			within(dialog)
				.getAllByRole('button')
				.filter((b) => b.querySelector('.truncate'))
				.map((b) => b.querySelector('.truncate')!.textContent)
		).toEqual(['Beto']);
	});

	it("playing: the custom category headers are reportable, built-in ones are not, others' answers do not exist yet", () => {
		const settings = makeSettings({
			categories: [{ id: 'nombre' }, { custom: 'Superhéroe' }]
		});
		open(
			makeView({
				phase: 'playing',
				players,
				settings,
				roundNum: 1,
				round: makeRound({
					letter: 'S',
					participants: ['p1', 'p2', 'bot:x'],
					answers: { p1: ['', ''] },
					filled: { p1: 0, p2: 1, 'bot:x': 1 }
				})
			})
		);
		expect(flags()).toEqual(['Report the category Superhéroe', 'Report or block a player']);
		// a hidden answer never gets an affordance: no answer button, no player button inside the fields
		expect(screen.queryByRole('button', { name: /answer/i })).toBeNull();
		for (const flag of screen.getAllByTestId('safety-button')) {
			expect(
				flag.closest('[role="group"]') === null ||
					flag.getAttribute('aria-label')?.startsWith('Report the category')
			).toBe(true);
		}
	});
});

describe('review', () => {
	const review = (options: ReviewOptions = {}) =>
		reviewView({ youId: 'p1', category: 1, ...options });

	it("every other player's answer has a flag, my own does not, an automated seat's does not", () => {
		open(asBot(review(), 'p3'));
		expect(flags().filter((f) => f?.endsWith(', about their answer'))).toEqual([
			'Report or block Beto, about their answer',
			'Report or block Dani, about their answer'
		]);
	});

	it('an empty answer is only offered as the player (nickname/avatar), never as an answer', () => {
		open(review({ answers: { p2: { 1: '' } } }));
		expect(flags()).toContain('Report or block Beto');
		expect(flags()).not.toContain('Report or block Beto, about their answer');
	});

	it('reporting an answer names round, category and author, and quotes what the reporter saw', async () => {
		const report = vi.spyOn(safetyApi, 'report').mockResolvedValue({ id: 'r1' });
		const view = review({ answers: { p2: { 1: 'Pais raro' } } });
		const session = open(view);
		await fireEvent.click(
			screen.getByRole('button', { name: 'Report or block Beto, about their answer' })
		);
		await fireEvent.click(await screen.findByRole('button', { name: 'Report this answer' }));
		await fireEvent.click(await screen.findByRole('radio', { name: 'Spam or ads' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(report).toHaveBeenCalledTimes(1));
		expect(report.mock.calls[0][0]).toMatchObject({
			kind: 'answer',
			code: session.code,
			roomInstanceId: 'room-instance-1',
			ref: { playerId: 'p2', round: view.round!.number, category: 1 },
			reason: 'spam',
			observedText: 'Pais raro'
		});
	});

	it('the AI note is reportable wherever it is shown, also on my own answer', () => {
		open(review({ judged: [1] }));
		const notes = flags().filter((f) => f?.startsWith('Report the AI explanation'));
		expect(notes.length).toBeGreaterThanOrEqual(3);
		expect(notes).toContain("Report the AI explanation of Ana's answer");
		expect(document.querySelectorAll('[data-testid="verdict"]')).toHaveLength(notes.length);
	});

	it('without a verdict there is no AI report affordance', () => {
		open(review());
		expect(flags().some((f) => f?.startsWith('Report the AI'))).toBe(false);
	});

	it('reporting the AI note sends kind ai_verdict with the note as the reporter read it', async () => {
		const report = vi.spyOn(safetyApi, 'report').mockResolvedValue({ id: 'r1' });
		const view = review({ judged: [1] });
		open(view);
		const verdict = view.round!.verdicts.find((v) => v.player === 'p2')!;
		await fireEvent.click(
			screen.getByRole('button', { name: "Report the AI explanation of Beto's answer" })
		);
		await fireEvent.click(await screen.findByRole('radio', { name: 'Something else' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(report).toHaveBeenCalledTimes(1));
		expect(report.mock.calls[0][0]).toMatchObject({
			kind: 'ai_verdict',
			ref: { playerId: 'p2', round: view.round!.number, category: 1 }
		});
		expect(report.mock.calls[0][0].observedText).toContain(verdict.reason!);
	});

	it("a merge proposal offers a flag for each other player's answer in it", () => {
		open(
			review({
				category: 3,
				proposals: [{ category: 3, playerA: 'p2', playerB: 'p3', proposedBy: 'p2', votes: [] }]
			})
		);
		expect(flags()).toContain("Report Beto's answer");
		expect(flags()).toContain("Report Caro's answer");
	});

	it('a proposal with my own answer offers no flag for it', () => {
		open(
			review({
				category: 3,
				proposals: [{ category: 3, playerA: 'p1', playerB: 'p3', proposedBy: 'p1', votes: [] }]
			})
		);
		expect(flags()).not.toContain("Report Ana's answer");
		expect(flags()).toContain("Report Caro's answer");
	});

	it('is well formed: one h1, names that contain the visible text, form fields with ids', () => {
		open(review({ judged: [1] }));
		expect(headingOnes(document.body)).toHaveLength(1);
		expect(labelNameMismatches(document.body)).toEqual([]);
		expect(fieldProblems(document.body)).toEqual([]);
	});
});

describe('round summary and final', () => {
	it.each(['round_summary', 'final'] as const)(
		"%s: every other player's row has a flag",
		(phase) => {
			open(asBot(reviewView({ youId: 'p1', phase }), 'p4'));
			expect(flags().filter((f) => f?.startsWith('Report or block '))).toEqual([
				'Report or block Beto',
				'Report or block Caro'
			]);
		}
	);

	it('a flag stays outside the expandable row button (no nested controls)', () => {
		open(reviewView({ youId: 'p1', phase: 'round_summary' }));
		for (const flag of screen.getAllByTestId('safety-button')) {
			expect(flag.closest('button:not([data-testid="safety-button"])')).toBeNull();
		}
	});

	it('block from a scoreboard row leaves the game and goes through the block API', async () => {
		const block = vi.spyOn(safetyApi, 'block').mockResolvedValue({
			block: { playerId: 'p2', name: 'Beto', blockedAt: 1 },
			leftRooms: ['KT7P']
		});
		const session = open(reviewView({ youId: 'p1', phase: 'final' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Report or block Beto' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Block and leave' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Block and leave' }));
		await waitFor(() => expect(block).toHaveBeenCalledWith({ playerId: 'p2', name: 'Beto' }));
		await waitFor(() => expect(session.status).toBe('closed'));
		expect(session.sent.map((c) => c.cmd)).toEqual(['leaveLocally']);
	});
});
