// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeStopVote, makeView, player } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import type { Phase, Player, StopVoteView, View } from '$lib/protocol';
import { toasts } from '$lib/toast.svelte';
import Playing from './Playing.svelte';

const NOW = Date.parse('2026-05-01T12:00:00Z');
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

const LABELS = {
	es: ['Nombre', 'País', 'Animal'],
	en: ['Name', 'Country', 'Animal'],
	nl: ['Naam', 'Land', 'Dier']
};

interface Setup {
	lang?: 'es' | 'en' | 'nl';
	phase?: Phase;
	youId?: string;
	mine?: string[];
	stopVote?: boolean;
	/** The open vote; `ends` is relative to NOW. */
	vote?: Partial<StopVoteView> & { ends?: number };
	players?: Player[];
	status?: FakeSession['status'];
}

function roster(): Player[] {
	return [
		player('p1', 'Ana'),
		player('p2', 'Beto'),
		player('p3', 'Caro'),
		player('p4', 'Dani'),
		player('p5', 'Eli')
	];
}

function makePlayingView(setup: Setup = {}): View {
	const { lang = 'es', phase = 'playing', youId = 'p1', mine, vote, stopVote = true } = setup;
	const answers: Record<string, string[]> = {};
	if (mine) answers[youId] = mine;
	const { ends = 15_000, ...rest } = vote ?? {};
	return makeView({
		phase,
		players: setup.players ?? roster(),
		youId,
		hostId: 'p1',
		settings: makeSettings({
			lang,
			rounds: 5,
			categories: [{ id: 'a' }, { id: 'b' }, { id: 'c' }],
			stopVote
		}),
		categoryLabels: LABELS[lang],
		roundNum: 2,
		usedLetters: ['A', 'F'],
		round: makeRound({
			number: 2,
			letter: 'F',
			// p5 watches this round
			participants: ['p1', 'p2', 'p3', 'p4'],
			answers,
			filled: { p2: 1, p3: 1, p4: 0 },
			stopVote: vote
				? makeStopVote({ callerId: 'p1', voters: ['p2', 'p3', 'p4'], endsAt: iso(ends), ...rest })
				: undefined
		})
	});
}

function setup(s: Setup = {}) {
	const session = new FakeSession(makePlayingView(s));
	session.frozenNow = NOW;
	if (s.status) session.status = s.status;
	const view = render(Playing, { session });
	return { session, ...view };
}

const sent = (session: FakeSession) =>
	session.sent.filter((c) => c.cmd !== 'destroy').map((c) => [c.cmd, ...c.args]);

const main = () => screen.getByTestId('basta-button') as HTMLButtonElement;
const input = (label: string) => screen.getByLabelText(label) as HTMLInputElement;
const type = (label: string, value: string) => fireEvent.input(input(label), { target: { value } });
const hintText = () => document.getElementById('basta-hint')?.textContent?.trim();
const dialog = () => screen.queryByRole('alertdialog');
const prompt = () => screen.queryByTestId('stop-vote-sheet');
const banner = () => screen.queryByTestId('stop-wait-banner');

async function advance(session: FakeSession, ms: number) {
	if (session.frozenNow !== null) session.frozenNow += ms;
	await vi.advanceTimersByTimeAsync(ms);
	await tick();
}

/** Opens the confirmation the way a player does: focus the button, tap it. */
async function openConfirm() {
	main().focus();
	await fireEvent.click(main());
	return screen.getByRole('alertdialog');
}

const withVote = (session: FakeSession, s: Setup) => {
	session.setView(makePlayingView(s));
	return tick();
};

beforeEach(() => {
	vi.useFakeTimers();
	i18n.set('es');
	toasts.items = [];
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	Reflect.deleteProperty(navigator, 'vibrate');
});

describe('the main button with the vote to stop on', () => {
	it('is enabled with answers missing, keeps its label, loses the pulse and explains itself', () => {
		setup({ mine: ['Fede', '', ''] });
		expect(main().disabled).toBe(false);
		expect(main().textContent?.trim()).toBe('¡BASTA!');
		expect(main().classList.contains('basta-ready')).toBe(false);
		expect(main().className).toContain('bg-night-3');
		expect(main().getAttribute('aria-describedby')).toBe('basta-hint');
		expect(main().getAttribute('aria-haspopup')).toBe('dialog');
		expect(hintText()).toBe('Te faltan 2 · tocá para pedir que paren');
	});

	it('is enabled even with every field empty', () => {
		setup();
		expect(main().disabled).toBe(false);
		expect(hintText()).toBe('Te faltan 3 · tocá para pedir que paren');
	});

	it('says "Te falta 1" in the singular and follows what is typed', async () => {
		setup({ mine: ['Fede', 'Francia', ''] });
		expect(hintText()).toBe('Te falta 1 · tocá para pedir que paren');
		await type('Animal', 'Foca');
		expect(hintText()).toBe('¡Todo listo!');
	});

	it('reuses the wrong-letter hint and names the categories', () => {
		setup({ mine: ['Fede', 'Brasil', 'Azul'] });
		expect(main().disabled).toBe(false);
		expect(hintText()).toBe('Revisá la letra en: País, Animal · tocá para pedir que paren');
	});

	it('with everything valid it is exactly today: pulse, no confirmation, basta', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		expect(main().disabled).toBe(false);
		expect(main().classList.contains('basta-ready')).toBe(true);
		expect(main().className).toContain('bg-strawberry');
		expect(main().getAttribute('aria-haspopup')).toBeNull();
		expect(hintText()).toBe('¡Todo listo!');
		await fireEvent.click(main());
		expect(dialog()).toBeNull();
		expect(sent(session)).toEqual([['basta']]);
	});

	it.each([
		// stopVote, online, answers, enabled
		['on', 'online', 'missing', true],
		['on', 'online', 'complete', true],
		['on', 'offline', 'missing', false],
		['on', 'offline', 'complete', false],
		['off', 'online', 'missing', false],
		['off', 'online', 'complete', true],
		['off', 'offline', 'missing', false],
		['off', 'offline', 'complete', false]
	] as const)('stopVote %s, %s, answers %s: enabled = %s', async (vote, line, answers, enabled) => {
		const { session } = setup({
			stopVote: vote === 'on',
			mine: answers === 'complete' ? ['Fede', 'Francia', 'Foca'] : ['Fede', '', '']
		});
		if (line === 'offline') {
			session.status = 'reconnecting';
			await tick();
		}
		expect(main().disabled).toBe(!enabled);
	});

	it('keeps the old disabled behaviour and hint when the setting is off', () => {
		setup({ stopVote: false, mine: ['Fede', '', ''] });
		expect(main().disabled).toBe(true);
		expect(main().getAttribute('aria-haspopup')).toBeNull();
		expect(hintText()).toBe('Completá todas las categorías');
	});

	it('is disabled and says so while offline, even with the vote on', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		session.status = 'reconnecting';
		await tick();
		expect(main().disabled).toBe(true);
		expect(hintText()).toBe('Sin conexión: esperá a que vuelva');
		expect(input('Nombre').disabled).toBe(false);
	});

	it('spectators have no button at all', () => {
		setup({ youId: 'p5', mine: [] });
		expect(screen.queryByTestId('basta-button')).toBeNull();
	});

	it('is gone during the grace countdown', () => {
		setup({ phase: 'grace', mine: ['Fede', '', ''] });
		expect(screen.queryByTestId('basta-button')).toBeNull();
		expect(prompt()).toBeNull();
		expect(banner()).toBeNull();
	});

	it('is disabled while somebody else\u2019s vote is open and the viewer has answers missing', () => {
		setup({ youId: 'p2', mine: ['Fede', '', ''], vote: {} });
		expect(main().disabled).toBe(true);
	});

	it('still lets a viewer with everything valid call Basta during somebody else\u2019s vote', async () => {
		const { session } = setup({ youId: 'p2', mine: ['Fede', 'Francia', 'Foca'], vote: {} });
		expect(main().disabled).toBe(false);
		await fireEvent.click(main());
		expect(sent(session)).toEqual([['basta']]);
	});
});

describe('the confirmation sheet', () => {
	it('opens an accessible alert dialog that says how many are missing and wrong', async () => {
		setup({ mine: ['Fede', 'Brasil', ''] });
		const sheet = await openConfirm();
		expect(sheet.getAttribute('role')).toBe('alertdialog');
		expect(sheet.getAttribute('aria-modal')).toBe('true');
		expect(within(sheet).getByRole('heading', { name: '¿Pedir que paren?' })).toBeTruthy();
		expect(sheet.getAttribute('aria-labelledby')).toBeTruthy();
		const description = document.getElementById(sheet.getAttribute('aria-describedby')!)!;
		expect(description.textContent).toContain('Te falta 1 categoría.');
		expect(description.textContent).toContain('1 respuesta no empieza con F.');
		expect(description.textContent).toContain(
			'Todos tienen que aceptar en 15 s; si alguien dice que no, la ronda sigue.'
		);
	});

	it('counts several missing and several wrong answers', async () => {
		setup({ lang: 'es', mine: ['Fede', '', ''] });
		await openConfirm();
		expect(screen.getByText('Te faltan 2 categorías.')).toBeTruthy();
		expect(screen.queryByText(/no empiezan con/)).toBeNull();
	});

	it('moves the focus inside (on "keep playing") and has big targets', async () => {
		setup({ mine: ['Fede', '', ''] });
		const sheet = await openConfirm();
		const keep = within(sheet).getByRole('button', { name: 'Seguir jugando' });
		const confirm = within(sheet).getByRole('button', { name: 'Sí, pedir que paren' });
		expect(document.activeElement).toBe(keep);
		for (const button of [keep, confirm]) expect(button.className).toContain('min-h-14');
		expect(within(sheet).getByRole('button', { name: 'Cerrar' }).className).toContain('size-12');
	});

	it('Escape closes it, sends nothing and returns the focus to the button', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
		await tick();
		expect(dialog()).toBeNull();
		expect(document.activeElement).toBe(main());
		expect(sent(session)).toEqual([]);
		expect(main().disabled).toBe(false);
	});

	it('"keep playing" closes it, sends nothing and returns the focus to the button', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Seguir jugando' }));
		await tick();
		expect(dialog()).toBeNull();
		expect(document.activeElement).toBe(main());
		expect(sent(session)).toEqual([]);
	});

	it('the backdrop and the close button also mean "keep playing"', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Cerrar' }));
		expect(dialog()).toBeNull();
		await openConfirm();
		await fireEvent.click(screen.getByRole('alertdialog').parentElement!);
		expect(dialog()).toBeNull();
		expect(sent(session)).toEqual([]);
	});

	it('confirming flushes the pending answers first and then sends ask_stop once', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await type('País', 'Francia');
		await openConfirm();
		const confirm = screen.getByRole('button', { name: 'Sí, pedir que paren' });
		await fireEvent.click(confirm);
		await fireEvent.click(confirm);
		expect(sent(session)).toEqual([['setAnswer', 1, 'Francia'], ['askStop']]);
		expect(dialog()).toBeNull();
		expect(main().disabled).toBe(true);
		await advance(session, 1000);
		expect(sent(session)).toHaveLength(2);
	});

	it('sends nothing when the button is only tapped (the confirmation comes first)', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await fireEvent.click(main());
		expect(sent(session)).toEqual([]);
		expect(dialog()).toBeTruthy();
	});

	it('is re-armed when the server refuses the command', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		expect(main().disabled).toBe(true);
		session.fail('stop_vote_pending', 'ask_stop');
		await tick();
		expect(main().disabled).toBe(false);
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		expect(sent(session).filter(([cmd]) => cmd === 'askStop')).toHaveLength(2);
	});

	it('ignores an error of another command', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		session.fail('not_host', 'kick');
		await tick();
		expect(main().disabled).toBe(true);
	});

	it('closes by itself when somebody else opens a vote first', async () => {
		const { session } = setup({ youId: 'p2', mine: ['Fede', '', ''] });
		await openConfirm();
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: { callerId: 'p3' } });
		expect(dialog()).toBeNull();
		expect(prompt()).toBeTruthy();
	});

	it('closes by itself when the round goes to grace', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		session.setView(
			makePlayingView({ phase: 'grace', mine: ['Fede', '', ''] }) as View & { round: object }
		);
		session.view!.round!.callerId = 'p2';
		session.view!.round!.graceEndsAt = iso(3000);
		await tick();
		expect(dialog()).toBeNull();
	});

	it('closes by itself when the connection drops', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		session.status = 'reconnecting';
		await tick();
		expect(dialog()).toBeNull();
	});
});

describe('the asker waiting', () => {
	const asker = (over: Setup = {}) =>
		setup({ mine: ['Fede', '', ''], vote: { yes: ['p2'] }, ...over });

	it('shows the banner in the footer stack with the tally and a live countdown', async () => {
		const { session } = asker();
		const bar = banner()!;
		expect(bar.textContent).toContain('Esperando a los demás');
		expect(bar.textContent).toContain('1 de 3 dijeron que sí');
		const timer = within(bar).getByRole('timer', { name: 'Tiempo para votar' });
		expect(timer.textContent?.trim()).toBe('15');
		expect(timer.getAttribute('aria-live')).toBeNull();
		expect(bar.closest('footer')).toBeTruthy();
		expect(bar.closest('footer')).toBe(main().closest('footer'));
		await advance(session, 3000);
		expect(timer.textContent?.trim()).toBe('12');
		await advance(session, 12_000);
		expect(timer.textContent?.trim()).toBe('0');
	});

	it('computes the countdown from the server clock (offset), not from when the banner appeared', () => {
		const session = new FakeSession(
			makePlayingView({ mine: ['Fede', '', ''], vote: { ends: 6400 } })
		);
		session.frozenNow = NOW;
		render(Playing, { session });
		expect(screen.getByRole('timer', { name: 'Tiempo para votar' }).textContent?.trim()).toBe('7');
	});

	it('never shows more than the 15 s of a vote (a skewed clock)', () => {
		setup({ mine: ['Fede', '', ''], vote: { ends: 40_000 } });
		expect(screen.getByRole('timer', { name: 'Tiempo para votar' }).textContent?.trim()).toBe('15');
	});

	it('updates the tally from the state', async () => {
		const { session } = asker();
		await withVote(session, { mine: ['Fede', '', ''], vote: { yes: ['p2', 'p3'] } });
		expect(banner()!.textContent).toContain('2 de 3 dijeron que sí');
	});

	it('follows the electorate: a voter who left drops out of the total', async () => {
		const { session } = asker();
		await withVote(session, { mine: ['Fede', '', ''], vote: { voters: ['p2'], yes: ['p2'] } });
		expect(banner()!.textContent).toContain('1 de 1 dijeron que sí');
	});

	it('hides the tally when nobody else is connected', async () => {
		const { session } = asker();
		await withVote(session, { mine: ['Fede', '', ''], vote: { voters: [], yes: [] } });
		expect(banner()!.textContent).toContain('Esperando a los demás');
		expect(banner()!.textContent).not.toContain('dijeron');
	});

	it('disables the main button, says to keep filling in and keeps the fields editable', async () => {
		const { session } = asker();
		expect(main().disabled).toBe(true);
		expect(main().getAttribute('aria-haspopup')).toBeNull();
		expect(hintText()).toBe('Seguí completando mientras esperás');
		expect(input('País').disabled).toBe(false);
		await type('País', 'Francia');
		await fireEvent.blur(input('País'));
		expect(sent(session)).toEqual([['setAnswer', 1, 'Francia']]);
	});

	it('lets the asker call Basta once everything is filled in meanwhile (it ends their own vote)', async () => {
		const { session } = asker();
		expect(main().disabled).toBe(true);
		await type('País', 'Francia');
		await type('Animal', 'Foca');
		expect(main().disabled).toBe(false);
		expect(main().getAttribute('aria-haspopup')).toBeNull();
		expect(hintText()).toBe('¡Todo listo!');
		await fireEvent.click(main());
		expect(sent(session).at(-1)).toEqual(['basta']);
	});

	it('shows no voter prompt to the asker', () => {
		asker();
		expect(prompt()).toBeNull();
		expect(screen.queryByTestId('stop-vote-yes')).toBeNull();
	});

	it('goes away with the vote and the button works again at once (no cooldown)', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		await withVote(session, { mine: ['Fede', '', ''], vote: {} });
		expect(banner()).toBeTruthy();
		await withVote(session, { mine: ['Fede', '', ''] });
		expect(banner()).toBeNull();
		expect(main().disabled).toBe(false);
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		expect(sent(session).filter(([cmd]) => cmd === 'askStop')).toHaveLength(2);
	});

	it('is state driven: a reload in the middle of the vote shows the banner again', () => {
		asker();
		expect(banner()).toBeTruthy();
		expect(main().disabled).toBe(true);
	});

	it('hands over to the Basta overlay when the vote passes', async () => {
		const { session } = asker();
		session.setView(makePlayingView({ phase: 'grace', mine: ['Fede', '', ''] }));
		session.view!.round!.callerId = 'p1';
		session.view!.round!.graceEndsAt = iso(3000);
		await tick();
		expect(banner()).toBeNull();
		expect(screen.getByTestId('basta-splash').textContent).toContain('Ana');
		expect(screen.getByTestId('grace-banner').textContent).toContain('¡Cantaste BASTA!');
	});
});

describe('toasts for a failed vote', () => {
	const rejected = (session: FakeSession, reason: string, playerId = 'p1') =>
		session.emit({ type: 'stop_rejected', playerId, name: 'Ana', reason });

	it('tells the asker nobody agreed (declined)', async () => {
		const { session } = setup({ mine: ['Fede', '', ''], vote: {} });
		rejected(session, 'declined');
		await tick();
		expect(toasts.items.map((t) => t.text)).toEqual(['No todos aceptaron']);
	});

	it('tells the asker the time ran out (timeout)', async () => {
		const { session } = setup({ mine: ['Fede', '', ''], vote: {} });
		rejected(session, 'timeout');
		await tick();
		expect(toasts.items.map((t) => t.text)).toEqual(['Se acabó el tiempo']);
	});

	it('says nothing when the vote simply ended (ended)', async () => {
		const { session } = setup({ mine: ['Fede', '', ''], vote: {} });
		rejected(session, 'ended');
		rejected(session, 'something_new');
		await tick();
		expect(toasts.items).toEqual([]);
	});

	it('says nothing to the others, voters or not', async () => {
		const voter = setup({ youId: 'p2', mine: ['Fede', '', ''], vote: {} });
		rejected(voter.session, 'declined');
		await tick();
		expect(toasts.items).toEqual([]);
		voter.unmount();
		const spectator = setup({ youId: 'p5', vote: {} });
		rejected(spectator.session, 'timeout');
		await tick();
		expect(toasts.items).toEqual([]);
	});

	it('does not replay the events that were there before the screen opened (reload)', async () => {
		const session = new FakeSession(makePlayingView({ mine: ['Fede', '', ''] }));
		session.frozenNow = NOW;
		rejected(session, 'declined');
		render(Playing, { session });
		await tick();
		expect(toasts.items).toEqual([]);
	});

	it('shows each event once, even when several arrive together', async () => {
		const { session } = setup({ mine: ['Fede', '', ''], vote: {} });
		rejected(session, 'declined');
		rejected(session, 'timeout');
		await tick();
		await withVote(session, { mine: ['Fede', '', ''] });
		expect(toasts.items.map((t) => t.text)).toEqual(['No todos aceptaron', 'Se acabó el tiempo']);
	});

	it('re-arms the button the moment the asker hears the vote failed', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await openConfirm();
		await fireEvent.click(screen.getByRole('button', { name: 'Sí, pedir que paren' }));
		expect(main().disabled).toBe(true);
		rejected(session, 'declined');
		await tick();
		expect(main().disabled).toBe(false);
	});

	it('speaks English and Dutch without any Spanish', async () => {
		i18n.set('en');
		const en = setup({ lang: 'en', mine: ['Fred', '', ''], vote: {} });
		rejected(en.session, 'declined');
		rejected(en.session, 'timeout');
		await tick();
		expect(toasts.items.map((t) => t.text)).toEqual(['Not everyone agreed', 'Time ran out']);
		en.unmount();
		toasts.items = [];
		i18n.set('nl');
		const nl = setup({ lang: 'nl', mine: ['Femke', '', ''], vote: {} });
		rejected(nl.session, 'declined');
		rejected(nl.session, 'timeout');
		await tick();
		expect(toasts.items.map((t) => t.text)).toEqual([
			'Niet iedereen ging akkoord',
			'De stemtijd is om'
		]);
	});
});

describe('the voter prompt', () => {
	const voter = (over: Setup = {}) =>
		setup({ youId: 'p2', mine: ['Fede', '', ''], vote: {}, ...over });

	it('asks "{name} pide parar. ¿Parar?" with a big yes and a big no and the countdown', async () => {
		const { session } = voter();
		const sheet = prompt()!;
		expect(sheet.textContent).toContain('Ana pide parar. ¿Parar?');
		const yes = within(sheet).getByRole('button', { name: 'Sí, parar' });
		const no = within(sheet).getByRole('button', { name: 'No, seguir' });
		for (const button of [yes, no]) expect(button.className).toContain('min-h-14');
		const timer = within(sheet).getByRole('timer', { name: 'Tiempo para votar' });
		expect(timer.textContent?.trim()).toBe('15');
		await advance(session, 4000);
		expect(timer.textContent?.trim()).toBe('11');
		expect(sheet.closest('footer')).toBe(main().closest('footer'));
	});

	it('is NOT modal: no dialog role, no trapped focus, fields stay usable', async () => {
		const { session } = voter();
		expect(dialog()).toBeNull();
		expect(screen.queryByRole('dialog')).toBeNull();
		expect(prompt()!.getAttribute('aria-modal')).toBeNull();
		expect(prompt()!.closest('[aria-modal]')).toBeNull();
		expect(document.body.style.overflow).not.toBe('hidden');
		input('País').focus();
		expect(document.activeElement).toBe(input('País'));
		await type('País', 'Francia');
		await fireEvent.blur(input('País'));
		expect(sent(session)).toEqual([['setAnswer', 1, 'Francia']]);
		expect(document.activeElement).not.toBe(screen.getByTestId('stop-vote-yes'));
	});

	it('announces the request assertively, vibrates and does not repeat it', async () => {
		const vibrate = vi.fn(() => true);
		Object.defineProperty(navigator, 'vibrate', { value: vibrate, configurable: true });
		const { session } = voter();
		await tick();
		expect(screen.getByRole('alert').textContent).toBe('Ana pide parar. ¿Parar?');
		expect(screen.getByRole('alert').getAttribute('aria-live')).toBe('assertive');
		expect(vibrate).toHaveBeenCalledTimes(1);
		await advance(session, 2000);
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: { yes: ['p3'] } });
		expect(vibrate).toHaveBeenCalledTimes(1);
	});

	it('does not vibrate with reduced motion, and survives a browser without vibration', async () => {
		const vibrate = vi.fn(() => true);
		Object.defineProperty(navigator, 'vibrate', { value: vibrate, configurable: true });
		vi.stubGlobal('matchMedia', (query: string) => ({ matches: query.includes('reduced-motion') }));
		voter();
		await tick();
		expect(vibrate).not.toHaveBeenCalled();
		vi.unstubAllGlobals();
		Reflect.deleteProperty(navigator, 'vibrate');
		expect(() => voter()).not.toThrow();
	});

	it('survives a vibrate that throws', () => {
		Object.defineProperty(navigator, 'vibrate', {
			value: () => {
				throw new Error('blocked');
			},
			configurable: true
		});
		expect(() => voter()).not.toThrow();
	});

	it('"yes" flushes the answers, sends vote_stop(true) once and waits for the others', async () => {
		const { session } = voter({ vote: { yes: ['p3'] } });
		await type('País', 'Francia');
		const yes = screen.getByRole('button', { name: 'Sí, parar' });
		await fireEvent.click(yes);
		await fireEvent.click(yes);
		await fireEvent.click(yes);
		expect(sent(session)).toEqual([
			['setAnswer', 1, 'Francia'],
			['voteStop', true]
		]);
		expect(screen.queryByRole('button', { name: 'Sí, parar' })).toBeNull();
		expect(prompt()!.textContent).toContain('Esperando a los demás (2/3)');
		expect(within(prompt()!).getByRole('timer', { name: 'Tiempo para votar' })).toBeTruthy();
	});

	it('"no" sends vote_stop(false) once and the prompt goes away', async () => {
		const { session } = voter();
		const no = screen.getByRole('button', { name: 'No, seguir' });
		await fireEvent.click(no);
		await fireEvent.click(no);
		expect(sent(session)).toEqual([['voteStop', false]]);
		expect(prompt()).toBeNull();
		expect(screen.queryByTestId('stop-vote-yes')).toBeNull();
	});

	it('a "yes" and a "no" tapped together send one command (the first wins)', async () => {
		const { session } = voter();
		const yes = screen.getByTestId('stop-vote-yes');
		const no = screen.getByTestId('stop-vote-no');
		void fireEvent.click(yes);
		void fireEvent.click(no);
		await tick();
		expect(sent(session)).toEqual([['voteStop', true]]);
	});

	it('shows the tally from the state after "yes" (n/total, live electorate)', async () => {
		const { session } = voter();
		await fireEvent.click(screen.getByTestId('stop-vote-yes'));
		expect(prompt()!.textContent).toContain('(1/3)');
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: { yes: ['p2', 'p4'] } });
		expect(prompt()!.textContent).toContain('Esperando a los demás (2/3)');
		await withVote(session, {
			youId: 'p2',
			mine: ['Fede', '', ''],
			vote: { voters: ['p2', 'p4'], yes: ['p2', 'p4'] }
		});
		expect(prompt()!.textContent).toContain('(2/2)');
	});

	it('after a reconnect a voter who already said yes sees the waiting state, not the buttons', () => {
		voter({ vote: { yes: ['p2'] } });
		expect(screen.queryByTestId('stop-vote-yes')).toBeNull();
		expect(prompt()!.textContent).toContain('Esperando a los demás (1/3)');
	});

	it('a reconnecting voter has not voted yet: the buttons are back', async () => {
		const { session } = voter({ vote: { yes: [] } });
		await fireEvent.click(screen.getByTestId('stop-vote-yes'));
		session.status = 'reconnecting';
		await tick();
		session.status = 'connected';
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: { yes: [] } });
		expect(screen.getByTestId('stop-vote-yes')).toBeTruthy();
	});

	it('is re-armed when the server refuses the vote', async () => {
		const { session } = voter();
		await fireEvent.click(screen.getByTestId('stop-vote-yes'));
		expect(screen.queryByTestId('stop-vote-yes')).toBeNull();
		session.fail('no_stop_vote', 'vote_stop');
		await tick();
		expect(screen.getByTestId('stop-vote-yes')).toBeTruthy();
		await fireEvent.click(screen.getByTestId('stop-vote-no'));
		expect(sent(session).filter(([cmd]) => cmd === 'voteStop')).toEqual([
			['voteStop', true],
			['voteStop', false]
		]);
	});

	it('goes away with the vote (state), whatever the reason', async () => {
		const { session } = voter();
		expect(prompt()).toBeTruthy();
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''] });
		expect(prompt()).toBeNull();
		// nobody's vote is open any more: with answers missing the viewer may ask in turn
		expect(main().disabled).toBe(false);
	});

	it('a second vote of the same player shows the prompt and announces it again', async () => {
		const vibrate = vi.fn(() => true);
		Object.defineProperty(navigator, 'vibrate', { value: vibrate, configurable: true });
		const { session } = voter();
		await fireEvent.click(screen.getByTestId('stop-vote-no'));
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''] });
		expect(prompt()).toBeNull();
		expect(screen.getByRole('alert').textContent).not.toContain('pide parar');
		await advance(session, 20_000);
		await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: { ends: 15_000 } });
		expect(prompt()).toBeTruthy();
		expect(screen.getByTestId('stop-vote-yes')).toBeTruthy();
		expect(screen.getByRole('alert').textContent).toBe('Ana pide parar. ¿Parar?');
		expect(vibrate).toHaveBeenCalledTimes(2);
	});

	it('disables the buttons while offline', async () => {
		const { session } = voter();
		session.status = 'reconnecting';
		await tick();
		expect((screen.getByTestId('stop-vote-yes') as HTMLButtonElement).disabled).toBe(true);
		expect((screen.getByTestId('stop-vote-no') as HTMLButtonElement).disabled).toBe(true);
		await fireEvent.click(screen.getByTestId('stop-vote-yes'));
		expect(sent(session)).toEqual([]);
	});

	it('a voter with answers missing can not call Basta meanwhile, but can still type', async () => {
		voter();
		expect(main().disabled).toBe(true);
		await type('Animal', 'Foca');
		expect(input('Animal').value).toBe('Foca');
	});

	it('keeps the focused field in view when the prompt appears (footer grows)', async () => {
		const { session } = setup({ youId: 'p2', mine: ['Fede', '', ''] });
		const scrollIntoView = vi.fn();
		Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
			value: scrollIntoView,
			configurable: true
		});
		try {
			input('Animal').focus();
			await withVote(session, { youId: 'p2', mine: ['Fede', '', ''], vote: {} });
			await advance(session, 200);
			expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
		} finally {
			Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
		}
	});

	it('breaks a very long name instead of overflowing', () => {
		const players = roster();
		players[0] = player('p1', 'Maximiliaan-Bartholomeus-van-der-Berg');
		setup({ youId: 'p2', players, mine: ['Fede', '', ''], vote: {} });
		const title = document.getElementById('stop-vote-title')!;
		expect(title.textContent).toContain('Maximiliaan-Bartholomeus-van-der-Berg');
		expect(title.className).toContain('break-words');
		expect(title.className).toContain('min-w-0');
	});
});

describe('spectators and players outside the vote', () => {
	it('a spectator sees a small info line and no buttons or fields', () => {
		setup({ youId: 'p5', vote: {} });
		const line = screen.getByTestId('stop-vote-onlooker');
		expect(line.textContent?.trim()).toBe('Ana pidió parar…');
		expect(prompt()).toBeNull();
		expect(banner()).toBeNull();
		expect(screen.queryAllByRole('textbox')).toHaveLength(0);
		expect(screen.queryByTestId('basta-button')).toBeNull();
		expect(screen.queryByRole('button')).toBeNull();
	});

	it('the line goes away with the vote', async () => {
		const { session } = setup({ youId: 'p5', vote: {} });
		session.setView(makePlayingView({ youId: 'p5' }));
		await tick();
		expect(screen.queryByTestId('stop-vote-onlooker')).toBeNull();
	});

	it('a participant who is not in the electorate (offline when it opened) sees the line, not the buttons', () => {
		setup({ youId: 'p2', mine: ['Fede', '', ''], vote: { voters: ['p3', 'p4'] } });
		expect(screen.getByTestId('stop-vote-onlooker').textContent?.trim()).toBe('Ana pidió parar…');
		expect(prompt()).toBeNull();
		expect(main().disabled).toBe(true);
	});

	it('a spectator is not announced anything and does not vibrate', async () => {
		const vibrate = vi.fn(() => true);
		Object.defineProperty(navigator, 'vibrate', { value: vibrate, configurable: true });
		setup({ youId: 'p5', vote: {} });
		await tick();
		expect(vibrate).not.toHaveBeenCalled();
		expect(screen.getByRole('alert').textContent).not.toContain('pide parar');
	});
});

describe('English', () => {
	beforeEach(() => i18n.set('en'));
	const noSpanish = () => expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]|basta/i);

	it('says Stop! on the button and explains the ask in the hint', () => {
		setup({ lang: 'en', mine: ['Fred', '', ''] });
		expect(main().textContent?.trim()).toBe('Stop!');
		expect(main().disabled).toBe(false);
		expect(hintText()).toBe('2 missing · tap to ask everyone to stop');
		noSpanish();
	});

	it('has the whole flow: hint (1 missing, wrong letter), confirmation, banner, prompt, onlooker', async () => {
		const first = setup({ lang: 'en', mine: ['Fred', 'France', ''] });
		expect(hintText()).toBe('1 missing · tap to ask everyone to stop');
		await type('Country', 'Brazil');
		expect(hintText()).toBe('Check the letter in: Country · tap to ask everyone to stop');
		await openConfirm();
		const sheet = screen.getByRole('alertdialog', { name: 'Ask everyone to stop?' });
		expect(sheet.textContent).toContain('1 category is missing.');
		expect(sheet.textContent).toContain("1 answer doesn't start with F.");
		expect(sheet.textContent).toContain(
			'Everyone has to agree within 15 s; if anyone says no, the round goes on.'
		);
		expect(within(sheet).getByRole('button', { name: 'Yes, ask everyone' })).toBeTruthy();
		expect(within(sheet).getByRole('button', { name: 'Keep playing' })).toBeTruthy();
		noSpanish();
		first.unmount();

		const asker = setup({ lang: 'en', mine: ['Fred', '', ''], vote: { yes: ['p2'] } });
		expect(banner()!.textContent).toContain('Waiting for the others');
		expect(banner()!.textContent).toContain('1 of 3 said yes');
		expect(hintText()).toBe('Keep filling in while you wait');
		noSpanish();
		asker.unmount();

		setup({ lang: 'en', youId: 'p2', mine: ['Fred', '', ''], vote: {} });
		expect(prompt()!.textContent).toContain('Ana wants to stop. Stop?');
		expect(screen.getByRole('button', { name: 'Yes, stop' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'No, keep going' })).toBeTruthy();
		expect(screen.getByRole('alert').textContent).toBe('Ana wants to stop. Stop?');
		noSpanish();
		await fireEvent.click(screen.getByRole('button', { name: 'Yes, stop' }));
		expect(prompt()!.textContent).toContain('Waiting for the others (1/3)');
		noSpanish();
	});

	it('a spectator reads "Ana asked to stop…"', () => {
		setup({ lang: 'en', youId: 'p5', vote: {} });
		expect(screen.getByTestId('stop-vote-onlooker').textContent?.trim()).toBe('Ana asked to stop…');
		noSpanish();
	});

	it('offline and with the setting off it is the old disabled button', async () => {
		const off = setup({ lang: 'en', stopVote: false, mine: ['Fred', '', ''] });
		expect(main().disabled).toBe(true);
		expect(hintText()).toBe('Fill in every category');
		off.unmount();
		const { session } = setup({ lang: 'en', mine: ['Fred', '', ''] });
		session.status = 'reconnecting';
		await tick();
		expect(main().disabled).toBe(true);
		expect(hintText()).toBe("You're offline: wait for the connection");
		noSpanish();
	});
});

describe('Dutch', () => {
	beforeEach(() => i18n.set('nl'));
	const noSpanish = () => {
		expect(document.body.textContent).not.toMatch(/[¡¿ñ]/);
		expect(document.body.textContent).not.toMatch(/basta/i);
	};

	it('says Stop! on the button and explains the ask in the hint', () => {
		setup({ lang: 'nl', mine: ['Femke', '', ''] });
		expect(main().textContent?.trim()).toBe('Stop!');
		expect(main().disabled).toBe(false);
		expect(hintText()).toBe('Nog 2 open · tik om iedereen te vragen om te stoppen');
		noSpanish();
	});

	it('has the whole flow in Dutch', async () => {
		const first = setup({ lang: 'nl', mine: ['Femke', 'Frankrijk', ''] });
		expect(hintText()).toBe('Nog 1 open · tik om iedereen te vragen om te stoppen');
		await type('Land', 'Brazilië');
		expect(hintText()).toBe('Check de letter bij: Land · tik om iedereen te vragen om te stoppen');
		await openConfirm();
		const sheet = screen.getByRole('alertdialog', { name: 'Vraag iedereen om te stoppen?' });
		expect(sheet.textContent).toContain('Nog 1 categorie niet ingevuld.');
		expect(sheet.textContent).toContain('1 antwoord begint niet met F.');
		expect(sheet.textContent).toContain(
			'Iedereen moet binnen 15 s instemmen; zegt iemand nee, dan gaat de ronde door.'
		);
		expect(within(sheet).getByRole('button', { name: 'Ja, vraag iedereen' })).toBeTruthy();
		expect(within(sheet).getByRole('button', { name: 'Verder spelen' })).toBeTruthy();
		noSpanish();
		first.unmount();

		const asker = setup({ lang: 'nl', mine: ['Femke', '', ''], vote: { yes: ['p2'] } });
		expect(banner()!.textContent).toContain('Wachten op de anderen');
		expect(banner()!.textContent).toContain('1 van 3 zeiden ja');
		expect(hintText()).toBe('Vul gerust verder in terwijl je wacht');
		noSpanish();
		asker.unmount();

		const players = roster();
		players[0] = player('p1', 'Maximiliaan-Bartholomeus');
		setup({ lang: 'nl', players, youId: 'p2', mine: ['Femke', '', ''], vote: {} });
		expect(prompt()!.textContent).toContain('Maximiliaan-Bartholomeus wil stoppen. Stoppen?');
		expect(screen.getByRole('button', { name: 'Ja, stoppen' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Nee, doorgaan' })).toBeTruthy();
		noSpanish();
	});

	it('a spectator reads "Ana vroeg om te stoppen…"', () => {
		setup({ lang: 'nl', youId: 'p5', vote: {} });
		expect(screen.getByTestId('stop-vote-onlooker').textContent?.trim()).toBe(
			'Ana vroeg om te stoppen…'
		);
		noSpanish();
	});
});
