// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fieldProblems, labelNameMismatches } from '$lib/a11y.testkit';
import { i18n } from '$lib/i18n.svelte';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView, player } from '$lib/game/fixtures';
import { toasts } from '$lib/toast.svelte';
import { BlocksStore } from './blocks.svelte';
import type { SafetyRequest } from './context';
import { apiError, deferred, FakeSafetyApi } from './safety.testkit';
import SafetySheet from './SafetySheet.svelte';

function setup(request: SafetyRequest, session = lobby()) {
	const api = new FakeSafetyApi();
	const blocks = new BlocksStore(api);
	const onclose = vi.fn();
	render(SafetySheet, { session, request, onclose, api, blocks });
	return { api, blocks, onclose, session };
}

function lobby() {
	const session = new FakeSession(
		makeView({
			players: [
				player('p1', 'Ana'),
				player('p2', 'Beto'),
				player('p3', 'Caro'),
				player('bot:7f1c', 'Robin'),
				player('p5', 'Dani', { left: true })
			]
		})
	);
	session.roomInstanceId = 'room-instance-9';
	return session;
}

const beto = { id: 'p2', name: 'Beto' };
const answer = { round: 2, category: 1, text: 'Perro' };
const dialog = () => screen.getByRole('dialog');

beforeEach(() => {
	i18n.set('en');
	toasts.items = [];
});
afterEach(() => cleanup());

describe('players step', () => {
	it('lists the other players only: not me, not an automated seat, not who left', async () => {
		setup({ start: 'players' });
		expect(dialog().getAttribute('aria-modal')).toBe('true');
		const rows = within(screen.getByTestId('safety-sheet')).getAllByRole('button');
		expect(rows.map((r) => r.querySelector('.truncate')?.textContent)).toEqual(['Beto', 'Caro']);
		expect(document.activeElement).toBe(rows[0]);
	});

	it('says so when there is nobody else', () => {
		setup({ start: 'players' }, new FakeSession(makeView({ players: [player('p1', 'Ana')] })));
		expect(screen.getByText('There are no other players.')).toBeTruthy();
	});

	it('picking a player opens their menu and Back returns to the list', async () => {
		setup({ start: 'players' });
		await fireEvent.click(screen.getByRole('button', { name: 'Caro' }));
		expect(await screen.findByRole('heading', { name: 'Options for Caro' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		expect(await screen.findByRole('heading', { name: 'Report or block' })).toBeTruthy();
	});
});

describe('person menu', () => {
	it('offers the answer (when shown), the nickname and block, in one dialog', () => {
		setup({ start: 'person', person: beto, answer });
		expect(screen.getByRole('heading', { name: 'Options for Beto' })).toBeTruthy();
		expect(
			within(screen.getByTestId('safety-sheet'))
				.getAllByRole('button')
				.map((b) => b.textContent?.trim())
		).toEqual(['Report this answer', 'Report nickname or avatar', 'Block and leave']);
	});

	it('without an answer there is no answer entry', () => {
		setup({ start: 'person', person: beto });
		expect(screen.queryByRole('button', { name: 'Report this answer' })).toBeNull();
	});
});

describe('report form', () => {
	async function openAnswerReport() {
		const ctx = setup({ start: 'person', person: beto, answer });
		await fireEvent.click(screen.getByRole('button', { name: 'Report this answer' }));
		await screen.findByRole('group', { name: 'What is the problem?' });
		return ctx;
	}

	it('has the nine fixed reasons as radios, a 280-character comment with a counter, and a form that is well formed', async () => {
		await openAnswerReport();
		const radios = screen.getAllByRole('radio');
		expect(radios.map((r) => (r as HTMLInputElement).value)).toEqual([
			'harassment',
			'hate',
			'sexual',
			'child_safety',
			'violence',
			'self_harm',
			'spam',
			'impersonation',
			'other'
		]);
		expect(screen.getByRole('radio', { name: 'A child at risk' })).toBeTruthy();
		expect(radios.every((r) => !(r as HTMLInputElement).checked)).toBe(true);
		const comment = screen.getByLabelText('Comment (optional)') as HTMLTextAreaElement;
		expect(comment.maxLength).toBe(280);
		expect(screen.getByText('0/280')).toBeTruthy();
		await fireEvent.input(comment, { target: { value: 'abc' } });
		expect(screen.getByText('3/280')).toBeTruthy();
		expect(screen.getByText("Beto's answer: Perro")).toBeTruthy();
		expect(labelNameMismatches(dialog())).toEqual([]);
		expect(fieldProblems(dialog())).toEqual([]);
		for (const label of screen.getAllByRole('radio').map((r) => r.closest('label')!)) {
			expect(label.className).toContain('min-h-12');
		}
	});

	it('Send without a reason asks for one and sends nothing', async () => {
		const { api } = await openAnswerReport();
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		expect((await screen.findByRole('alert')).textContent).toBe('Choose a reason to continue.');
		expect(api.reports).toHaveLength(0);
	});

	it('sends the report, says "sent" only after the answer, closes the sheet and toasts', async () => {
		const { api, onclose } = await openAnswerReport();
		const pending = deferred<{ id: string }>();
		api.reportAnswers = [pending.promise];
		await fireEvent.click(screen.getByRole('radio', { name: 'Harassment or insults' }));
		await fireEvent.input(screen.getByLabelText('Comment (optional)'), {
			target: { value: 'rude' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		// in flight: busy, nothing claimed
		expect(screen.getByRole('button', { name: 'Sending…' }).getAttribute('aria-busy')).toBe('true');
		expect(screen.getByRole('status').textContent).toBe('Sending the report…');
		expect(onclose).not.toHaveBeenCalled();
		expect(toasts.items).toHaveLength(0);
		pending.resolve({ id: 'r1' });
		await waitFor(() => expect(onclose).toHaveBeenCalledTimes(1));
		expect(toasts.items.map((t) => t.text)).toEqual(['Report sent. Thank you!']);
		expect(api.reports).toHaveLength(1);
		expect(api.reports[0]).toMatchObject({
			kind: 'answer',
			code: 'KT7P',
			roomInstanceId: 'room-instance-9',
			ref: { playerId: 'p2', round: 2, category: 1 },
			reason: 'harassment',
			comment: 'rude'
		});
	});

	it.each([
		[429, 'rate_limited', "You're going too fast. Wait a moment."],
		[503, 'server_busy', 'The server is busy. Try again in a little while.'],
		[403, 'not_in_room', 'Only people who are or were in this game can report it.']
	])(
		'on %s keeps the draft, shows %s and retries with the same key',
		async (status, code, text) => {
			const { api, onclose } = await openAnswerReport();
			api.reportAnswers = [apiError(status, code), { id: 'r1' }];
			await fireEvent.click(screen.getByRole('radio', { name: 'Spam or ads' }));
			await fireEvent.input(screen.getByLabelText('Comment (optional)'), {
				target: { value: 'ads' }
			});
			await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
			const alert = await screen.findByRole('alert');
			expect(alert.textContent).toContain(text);
			expect(alert.textContent).toContain('Your text is still here');
			// nothing was claimed and the draft is intact
			expect(onclose).not.toHaveBeenCalled();
			expect(toasts.items).toHaveLength(0);
			expect((screen.getByRole('radio', { name: 'Spam or ads' }) as HTMLInputElement).checked).toBe(
				true
			);
			expect((screen.getByLabelText('Comment (optional)') as HTMLTextAreaElement).value).toBe(
				'ads'
			);
			await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
			await waitFor(() => expect(onclose).toHaveBeenCalledTimes(1));
			expect(api.reports.map((r) => r.idempotencyKey)).toHaveLength(2);
			expect(api.reports[0].idempotencyKey).toBe(api.reports[1].idempotencyKey);
		}
	);

	it('Back returns to the menu and a new report is a new draft with a new key', async () => {
		const { api } = await openAnswerReport();
		api.reportAnswers = [apiError(503, 'server_busy'), { id: 'r2' }];
		await fireEvent.click(screen.getByRole('radio', { name: 'Hate or discrimination' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await screen.findByRole('alert');
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Report nickname or avatar' }));
		await fireEvent.click(await screen.findByRole('radio', { name: 'Hate or discrimination' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(api.reports).toHaveLength(2));
		expect(api.reports[1].kind).toBe('player');
		expect(api.reports[1].idempotencyKey).not.toBe(api.reports[0].idempotencyKey);
	});

	it('a custom label opens straight on the report, quoting the label as seen', async () => {
		const { api } = setup({ start: 'label', category: 6, label: 'Superhéroe' });
		expect(screen.getByRole('heading', { name: 'Report' })).toBeTruthy();
		expect(screen.getByText('Custom category: Superhéroe')).toBeTruthy();
		await fireEvent.click(screen.getByRole('radio', { name: 'Something else' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(api.reports).toHaveLength(1));
		expect(api.reports[0]).toMatchObject({
			kind: 'custom_label',
			ref: { category: 6, label: 'Superhéroe' },
			observedText: 'Superhéroe'
		});
		// no menu to go back to: the button cancels
		expect(screen.queryByRole('button', { name: 'Back' })).toBeNull();
	});

	it('the AI note opens straight on the report of kind ai_verdict (for anybody, the viewer included)', async () => {
		const { api } = setup({
			start: 'verdict',
			person: { id: 'p1', name: 'Ana' },
			verdict: { round: 2, category: 1, text: 'AI: doubtful: not a country' }
		});
		expect(screen.getByText("The AI explanation of Ana's answer")).toBeTruthy();
		await fireEvent.click(screen.getByRole('radio', { name: 'Something else' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(api.reports).toHaveLength(1));
		expect(api.reports[0]).toMatchObject({
			kind: 'ai_verdict',
			ref: { playerId: 'p1', round: 2, category: 1 },
			observedText: 'AI: doubtful: not a country'
		});
	});

	it('works with the terms not accepted: it never asks for them', async () => {
		const { api } = await openAnswerReport();
		await fireEvent.click(screen.getByRole('radio', { name: 'Violence or threats' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(api.reports).toHaveLength(1));
		expect(api.accepts).toHaveLength(0);
	});

	it('sends a child at risk with its own reason code', async () => {
		const { api } = await openAnswerReport();
		await fireEvent.click(screen.getByRole('radio', { name: 'A child at risk' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Send report' }));
		await waitFor(() => expect(api.reports).toHaveLength(1));
		expect(api.reports[0]).toMatchObject({ kind: 'answer', reason: 'child_safety' });
	});
});

describe('block and leave', () => {
	async function openBlock(session = lobby()) {
		const ctx = setup({ start: 'person', person: beto }, session);
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		await screen.findByRole('heading', { name: 'Block and leave?' });
		return ctx;
	}

	it('says exactly what happens before doing anything, and Cancel is focused first', async () => {
		const { api } = await openBlock();
		const text = screen.getByTestId('safety-sheet').textContent ?? '';
		expect(text).toContain('You leave this game right now.');
		expect(text).toContain('Beto is blocked for this browser identity');
		expect(text).toContain('Clearing cookies or switching browser can undo it.');
		expect(text).toContain('Beto is not told.');
		expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Cancel' }));
		expect(api.blocked).toHaveLength(0);
	});

	it('blocks, toasts, and leaves WITHOUT a second leave when the server already removed the seat', async () => {
		const session = lobby();
		const { api, blocks, onclose } = await openBlock(session);
		api.blockAnswer = {
			block: { playerId: 'p2', name: 'Beto', blockedAt: 5 },
			leftRooms: ['KT7P']
		};
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		await waitFor(() => expect(onclose).toHaveBeenCalled());
		expect(api.blocked).toEqual([{ playerId: 'p2', name: 'Beto' }]);
		expect(session.sent.map((c) => c.cmd)).toEqual(['leaveLocally']);
		expect(session.status).toBe('closed');
		expect(toasts.items.map((t) => t.text)).toEqual(['Beto blocked. You left the game.']);
		expect(blocks.list.map((b) => b.playerId)).toEqual(['p2']);
		expect(blocks.expecting).toBe('KT7P');
	});

	it('uses the normal leave when the server did not take this seat out', async () => {
		const session = lobby();
		const { api, onclose } = await openBlock(session);
		api.blockAnswer = { block: { playerId: 'p2', name: 'Beto', blockedAt: 5 }, leftRooms: [] };
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		await waitFor(() => expect(onclose).toHaveBeenCalled());
		expect(session.sent.map((c) => c.cmd)).toEqual(['leave']);
	});

	it('does nothing to a session that already closed', async () => {
		const session = lobby();
		const { api, onclose } = await openBlock(session);
		session.status = 'closed';
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		await waitFor(() => expect(onclose).toHaveBeenCalled());
		expect(api.blocked).toHaveLength(1);
		expect(session.sent).toEqual([]);
	});

	it.each([
		[400, 'invalid_block', "This player can't be blocked."],
		[
			409,
			'block_limit',
			"You've reached the maximum number of blocked players. Unblock someone and try again."
		],
		[0, 'network', "Can't reach the server."]
	])('on %s (%s) stays, explains, and does not leave', async (status, code, text) => {
		const session = lobby();
		const { api, blocks, onclose } = await openBlock(session);
		api.blockAnswer = apiError(status, code);
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		const alert = await screen.findByRole('alert');
		expect(alert.textContent).toBe(`Couldn't block: ${text}`);
		expect(onclose).not.toHaveBeenCalled();
		expect(session.sent).toEqual([]);
		expect(blocks.expecting).toBeNull();
		expect(blocks.list).toEqual([]);
	});

	it('while it works the buttons are disabled and a second press does not block twice', async () => {
		const { api } = await openBlock();
		const pending = deferred<never>();
		api.block.mockReturnValueOnce(pending.promise);
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		expect(screen.getByRole('button', { name: 'Blocking…' }).hasAttribute('disabled')).toBe(true);
		expect(screen.getByRole('button', { name: 'Cancel' }).hasAttribute('disabled')).toBe(true);
		expect(api.block).toHaveBeenCalledTimes(1);
	});

	it('needs no terms either', async () => {
		const { api } = await openBlock();
		await fireEvent.click(screen.getByRole('button', { name: 'Block and leave' }));
		await waitFor(() => expect(api.blocked).toHaveLength(1));
		expect(api.accepts).toHaveLength(0);
	});
});

describe('keyboard and languages', () => {
	it('Escape closes the sheet', async () => {
		const { onclose } = setup({ start: 'person', person: beto });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onclose).toHaveBeenCalled();
	});

	it.each([
		['es', 'Reportar esta respuesta', 'Bloquear y salir'],
		['nl', 'Dit antwoord melden', 'Blokkeren en weggaan']
	] as const)('speaks %s', (locale, report, block) => {
		i18n.set(locale);
		setup({ start: 'person', person: beto, answer });
		expect(screen.getByRole('button', { name: report })).toBeTruthy();
		expect(screen.getByRole('button', { name: block })).toBeTruthy();
	});
});
