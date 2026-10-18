// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import type { Phase, Player, RoundView, View } from '$lib/protocol';
import Playing from './Playing.svelte';

const NOW = Date.parse('2026-05-01T12:00:00Z');
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

const LABELS = { es: ['Nombre', 'País', 'Animal'], en: ['Name', 'Country', 'Animal'] };

interface Setup {
	phase?: Phase;
	lang?: 'es' | 'en';
	letter?: string;
	youId?: string;
	mine?: string[];
	round?: Partial<RoundView>;
	players?: Player[];
}

function makePlayers(): Player[] {
	return [
		player('p1', 'Ana'),
		player('p2', 'Beto'),
		player('p3', 'Caro', { connected: false }),
		player('p4', 'Dani')
	];
}

function makePlayingView(setup: Setup = {}): View {
	const { phase = 'playing', lang = 'es', letter = 'F', youId = 'p1', mine, round } = setup;
	const settings = makeSettings({
		lang,
		rounds: 5,
		categories: [{ id: 'a' }, { id: 'b' }, { id: 'c' }]
	});
	const answers: Record<string, string[]> = {};
	if (mine) answers[youId] = mine;
	return makeView({
		phase,
		players: setup.players ?? makePlayers(),
		youId,
		hostId: 'p1',
		settings,
		categoryLabels: LABELS[lang],
		roundNum: 2,
		usedLetters: ['A', letter],
		round: makeRound({
			number: 2,
			letter,
			participants: ['p1', 'p2', 'p3'],
			answers,
			filled: { p2: 2, p3: 3 },
			...round
		})
	});
}

function setup(s: Setup = {}) {
	const session = new FakeSession(makePlayingView(s));
	session.frozenNow = NOW;
	const view = render(Playing, { session });
	return { session, ...view };
}

const sent = (session: FakeSession) =>
	session.sent.filter((c) => c.cmd !== 'destroy').map((c) => [c.cmd, ...c.args]);

const input = (label: string) => screen.getByLabelText(label) as HTMLInputElement;
const type = (label: string, value: string) => fireEvent.input(input(label), { target: { value } });
const bastaButton = () => screen.queryByRole('button', { name: /basta|stop/i });

/** Advances the fake clock and the component's 250 ms ticker. */
async function advance(session: FakeSession, ms: number) {
	if (session.frozenNow !== null) session.frozenNow += ms;
	await vi.advanceTimersByTimeAsync(ms);
	await tick();
}

beforeEach(() => {
	vi.useFakeTimers();
	i18n.set('es');
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	Reflect.deleteProperty(navigator, 'vibrate');
});

describe('Playing: header and fields', () => {
	it('shows the letter, the round and one labelled field per category', async () => {
		setup();
		await tick();
		expect(screen.getByRole('img', { name: 'Letra F' }).textContent?.trim()).toBe('F');
		expect(screen.getByText('Ronda 2 de 5')).toBeTruthy();
		for (const label of LABELS.es) expect(input(label).tagName).toBe('INPUT');
		expect(screen.getAllByRole('textbox')).toHaveLength(3);
	});

	it('says "Ronda 2" when the game has no round limit', () => {
		const session = new FakeSession(makePlayingView());
		session.view!.settings.rounds = 0;
		render(Playing, { session });
		expect(screen.getByText('Ronda 2')).toBeTruthy();
	});

	it('configures the inputs for fast typing on a phone', () => {
		setup();
		const first = input('Nombre');
		expect(first.getAttribute('autocomplete')).toBe('off');
		expect(first.getAttribute('autocorrect')).toBe('off');
		expect(first.getAttribute('autocapitalize')).toBe('words');
		expect(first.getAttribute('spellcheck')).toBe('false');
		expect(first.getAttribute('maxlength')).toBe('40');
		expect(first.getAttribute('enterkeyhint')).toBe('next');
		expect(input('País').getAttribute('enterkeyhint')).toBe('next');
		expect(input('Animal').getAttribute('enterkeyhint')).toBe('done');
	});

	it('Enter moves to the next field and leaves the last one without calling Basta', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		input('Nombre').focus();
		await fireEvent.keyDown(input('Nombre'), { key: 'Enter' });
		expect(document.activeElement).toBe(input('País'));
		await fireEvent.keyDown(input('País'), { key: 'Enter' });
		expect(document.activeElement).toBe(input('Animal'));
		await fireEvent.keyDown(input('Animal'), { key: 'Enter' });
		expect(document.activeElement).not.toBe(input('Animal'));
		expect(sent(session).some(([cmd]) => cmd === 'basta')).toBe(false);
	});

	it('seeds the fields from the answers the server already has (reload / reconnect)', () => {
		setup({ mine: ['Fede', '', 'Foca'] });
		expect(input('Nombre').value).toBe('Fede');
		expect(input('País').value).toBe('');
		expect(input('Animal').value).toBe('Foca');
	});

	it('does not send anything for the seeded answers', async () => {
		const { session } = setup({ mine: ['Fede', '', 'Foca'] });
		await advance(session, 2000);
		expect(sent(session)).toEqual([]);
	});

	it('adopts a newer snapshot for untouched fields but never overwrites what is being typed', async () => {
		const { session } = setup({ mine: ['Fede', '', ''] });
		await type('País', 'Fr');
		session.setView(makePlayingView({ mine: ['Fede', 'Argentina', 'Foca'] }));
		await tick();
		expect(input('País').value).toBe('Fr');
		expect(input('Animal').value).toBe('Foca');
	});

	it('starts over with the new answers when the round changes', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		await type('Nombre', 'Fer');
		session.setView(
			makePlayingView({ letter: 'B', round: { number: 3 }, mine: ['', 'Brasil', ''] })
		);
		await tick();
		expect(input('Nombre').value).toBe('');
		expect(input('País').value).toBe('Brasil');
		await advance(session, 2000);
		expect(sent(session)).toEqual([]);
	});

	it('focuses the first field on devices with a precise pointer', () => {
		vi.stubGlobal('matchMedia', (query: string) => ({ matches: query.includes('pointer: fine') }));
		setup();
		expect(document.activeElement).toBe(input('Nombre'));
	});

	it('does not pop the on-screen keyboard on touch devices', () => {
		vi.stubGlobal('matchMedia', () => ({ matches: false }));
		setup();
		expect(document.activeElement).not.toBe(input('Nombre'));
	});

	it('marks wrong letters with a message and valid ones with a check', async () => {
		setup();
		await type('Nombre', 'Fede');
		await type('País', 'Brasil');
		expect(input('Nombre').getAttribute('aria-invalid')).toBeNull();
		expect(input('País').getAttribute('aria-invalid')).toBe('true');
		expect(screen.getByText(/tiene que empezar con F/i)).toBeTruthy();
		expect(screen.getByText('Respuesta válida')).toBeTruthy();
		expect(input('Animal').getAttribute('aria-describedby')).toBeNull();
	});
});

describe('Playing: sending answers', () => {
	it('debounces typing and then sends the latest text once', async () => {
		const { session } = setup();
		await type('Nombre', 'F');
		await advance(session, 200);
		await type('Nombre', 'Fe');
		await advance(session, 200);
		expect(sent(session)).toEqual([]);
		await advance(session, 100);
		expect(sent(session)).toEqual([['setAnswer', 0, 'Fe']]);
		await advance(session, 2000);
		expect(sent(session)).toHaveLength(1);
	});

	it('sends immediately on blur', async () => {
		const { session } = setup();
		await type('País', 'Francia');
		await fireEvent.blur(input('País'));
		expect(sent(session)).toEqual([['setAnswer', 1, 'Francia']]);
		await advance(session, 2000);
		expect(sent(session)).toHaveLength(1);
	});

	it('flushes every dirty field the moment the phase becomes grace', async () => {
		const { session } = setup({ mine: ['', '', ''] });
		await type('Nombre', 'Fede');
		await type('Animal', 'Foca');
		expect(sent(session)).toEqual([]);
		session.setView(
			makePlayingView({
				phase: 'grace',
				mine: ['', '', ''],
				round: { callerId: 'p2', graceEndsAt: iso(3000) }
			})
		);
		await tick();
		expect(sent(session)).toEqual([
			['setAnswer', 0, 'Fede'],
			['setAnswer', 2, 'Foca']
		]);
	});

	it('keeps sending while the grace countdown runs (short debounce) and flushes again at the lock', async () => {
		const { session } = setup();
		session.setView(
			makePlayingView({ phase: 'grace', round: { callerId: 'p2', graceEndsAt: iso(3000) } })
		);
		await tick();
		await type('Nombre', 'Fe');
		await advance(session, 100);
		expect(sent(session)).toEqual([['setAnswer', 0, 'Fe']]);
		await type('País', 'Fr');
		session.frozenNow = NOW + 3000;
		await vi.advanceTimersByTimeAsync(250);
		await tick();
		expect(sent(session)).toEqual([
			['setAnswer', 0, 'Fe'],
			['setAnswer', 1, 'Fr']
		]);
	});

	it('flushes when the page is hidden', async () => {
		const { session } = setup();
		await type('Nombre', 'Fede');
		Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
		document.dispatchEvent(new Event('visibilitychange'));
		Reflect.deleteProperty(document, 'visibilityState');
		expect(sent(session)).toEqual([['setAnswer', 0, 'Fede']]);
	});

	it('sends what the restarted server lost after a reconnect', async () => {
		const { session } = setup();
		await type('Nombre', 'Fede');
		await fireEvent.blur(input('Nombre'));
		session.sent.length = 0;
		session.status = 'reconnecting';
		await tick();
		expect(input('Nombre').disabled).toBe(false);
		session.setView(makePlayingView({ mine: ['', '', ''] }));
		session.status = 'connected';
		await tick();
		expect(sent(session)).toEqual([['setAnswer', 0, 'Fede']]);
	});

	it('does not send anything when the screen goes away with typing pending', async () => {
		const { session, unmount } = setup();
		await type('Nombre', 'Fede');
		unmount();
		await advance(session, 2000);
		expect(sent(session)).toEqual([]);
	});
});

describe('Playing: the Basta button', () => {
	it('is disabled while a category is empty and says what is missing', () => {
		setup({ mine: ['Fede', '', 'Foca'] });
		const button = bastaButton() as HTMLButtonElement;
		expect(button.disabled).toBe(true);
		expect(button.textContent?.trim()).toBe('¡BASTA!');
		expect(screen.getByText('Completá todas las categorías')).toBeTruthy();
		expect(button.getAttribute('aria-describedby')).toBe('basta-hint');
	});

	it('is disabled and names the categories whose answer starts with another letter', () => {
		setup({ mine: ['Fede', 'Brasil', 'Azul'] });
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText('Revisá la letra en: País, Animal')).toBeTruthy();
	});

	it('is disabled when every field is empty', () => {
		setup();
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
	});

	it('enables once every answer starts with the letter, ignoring accents and case', async () => {
		setup({ letter: 'A', mine: ['ana', 'Árbol', ''] });
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		await type('Animal', 'Águila');
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(false);
		expect(screen.getByText('¡Todo listo!')).toBeTruthy();
	});

	it('treats ñ as its own letter in a Spanish lobby', () => {
		setup({ letter: 'Ñ', lang: 'es', mine: ['Ñandú', 'Ñoquis', 'Nube'] });
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText('Revisá la letra en: Animal')).toBeTruthy();
	});

	it('treats a plain n as wrong for Ñ in es, and ñ as n in an English lobby', async () => {
		setup({ letter: 'N', lang: 'es', mine: ['Nora', 'Ñoquis', 'Nutria'] });
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText('Revisá la letra en: País')).toBeTruthy();
	});

	it('folds ñ to n when the lobby language is English', () => {
		i18n.set('en');
		setup({ letter: 'N', lang: 'en', mine: ['Nora', 'Ñoquis', 'Nutria'] });
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(false);
	});

	it('flushes the dirty fields and then calls basta exactly once', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', ''] });
		await type('Animal', 'Foca');
		const button = bastaButton() as HTMLButtonElement;
		expect(button.disabled).toBe(false);
		await fireEvent.click(button);
		await fireEvent.click(button);
		await fireEvent.click(button);
		expect(sent(session)).toEqual([['setAnswer', 2, 'Foca'], ['basta']]);
		expect(button.disabled).toBe(true);
		await advance(session, 1000);
		expect(sent(session)).toHaveLength(2);
	});

	it('flushes nothing extra when everything was already sent', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		await fireEvent.click(bastaButton()!);
		expect(sent(session)).toEqual([['basta']]);
	});

	it('is re-armed when the server refuses the call', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		await fireEvent.click(bastaButton()!);
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		session.fail('basta_not_allowed', 'basta');
		await tick();
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(false);
		await fireEvent.click(bastaButton()!);
		expect(sent(session).filter(([cmd]) => cmd === 'basta')).toHaveLength(2);
	});

	it('is disabled and explains it while offline, but the fields stay usable', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		session.status = 'reconnecting';
		await tick();
		expect((bastaButton() as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText('Sin conexión: esperá a que vuelva')).toBeTruthy();
		expect(input('Nombre').disabled).toBe(false);
		await type('Nombre', 'Fer');
		await fireEvent.blur(input('Nombre'));
		expect(sent(session)).toEqual([['setAnswer', 0, 'Fer']]);
	});

	it('is gone during the grace countdown (nobody can call it twice)', () => {
		setup({
			phase: 'grace',
			mine: ['Fede', 'Francia', 'Foca'],
			round: { callerId: 'p2', graceEndsAt: iso(3000) }
		});
		expect(bastaButton()).toBeNull();
	});
});

describe('Playing: time limit', () => {
	it('has no timer without a time limit', () => {
		setup();
		expect(screen.queryByRole('timer')).toBeNull();
	});

	it('shows the time left, updates it and never announces it every second', async () => {
		const { session } = setup({ round: { timeLimitEndsAt: iso(83_000) } });
		const timer = screen.getByRole('timer', { name: 'Tiempo restante' });
		expect(timer.textContent?.trim()).toBe('1:23');
		expect(timer.closest('[aria-live="assertive"]')).toBeNull();
		await advance(session, 3000);
		expect(timer.textContent?.trim()).toBe('1:20');
	});

	it('turns tangerine in the last 10 seconds and strawberry in the last 5', async () => {
		const { session } = setup({ round: { timeLimitEndsAt: iso(30_000) } });
		const timer = screen.getByRole('timer', { name: 'Tiempo restante' });
		expect(timer.className).toContain('bg-night-3');
		await advance(session, 20_000);
		expect(timer.textContent?.trim()).toBe('0:10');
		expect(timer.className).toContain('bg-tangerine');
		await advance(session, 5000);
		expect(timer.textContent?.trim()).toBe('0:05');
		expect(timer.className).toContain('bg-strawberry');
	});

	it('announces the last 10 seconds once, politely', async () => {
		const { session, container } = setup({ round: { timeLimitEndsAt: iso(20_000) } });
		const polite = container.ownerDocument.querySelector('[aria-live="polite"]')!;
		expect(polite.textContent).toBe('');
		await advance(session, 11_000);
		expect(polite.textContent).toBe('Quedan 10 segundos');
		await advance(session, 3000);
		expect(polite.textContent).toBe('Quedan 10 segundos');
	});

	it('ignores the zero time as "no limit"', () => {
		setup({ round: { timeLimitEndsAt: '0001-01-01T00:00:00Z' } });
		expect(screen.queryByRole('timer')).toBeNull();
	});
});

describe('Playing: other players progress', () => {
	it('shows everyone still in the game with the server counts and dims the offline ones', () => {
		setup();
		const list = screen.getByRole('list', { name: 'Progreso de los jugadores' });
		const items = within(list).getAllByRole('listitem');
		expect(
			items.map((li) => li.textContent?.replace(/\p{Extended_Pictographic}|\s+/gu, ' ').trim())
		).toEqual(['0/3 Vos: 0 de 3', '2/3 Beto: 2 de 3', '3/3 Caro: 3 de 3 (sin conexión)']);
	});

	it('counts the own answers locally while typing', async () => {
		setup();
		await type('Nombre', 'Fede');
		expect(screen.getByText('Vos: 1 de 3')).toBeTruthy();
		await type('País', 'Brasil');
		expect(screen.getByText('Vos: 2 de 3')).toBeTruthy();
		await type('Nombre', '');
		expect(screen.getByText('Vos: 1 de 3')).toBeTruthy();
	});

	it('follows the server counts of the others', async () => {
		const { session } = setup();
		session.setView(makePlayingView({ round: { filled: { p2: 3, p3: 3 } } }));
		await tick();
		expect(screen.getByText('Beto: 3 de 3')).toBeTruthy();
	});
});

describe('Playing: spectator', () => {
	it('sees the letter and the others progress but has no fields and no Basta', () => {
		setup({
			players: [...makePlayers(), player('p5', 'Eli')],
			youId: 'p5',
			round: { timeLimitEndsAt: iso(60_000) }
		});
		expect(
			screen.getByText(/esta ronda la mirás desde afuera; te sumás en la próxima/i)
		).toBeTruthy();
		expect(screen.queryAllByRole('textbox')).toHaveLength(0);
		expect(bastaButton()).toBeNull();
		expect(screen.getByRole('img', { name: 'Letra F' })).toBeTruthy();
		expect(screen.getByText('Beto: 2 de 3')).toBeTruthy();
		expect(screen.queryByText(/Vos:/)).toBeNull();
		expect(screen.getByRole('timer', { name: 'Tiempo restante' }).textContent?.trim()).toBe('1:00');
	});

	it('does not send answers', async () => {
		const { session } = setup({ players: [...makePlayers(), player('p5', 'Eli')], youId: 'p5' });
		await advance(session, 2000);
		expect(sent(session)).toEqual([]);
	});
});

describe('Playing: letter reveal and announcements', () => {
	it('announces the letter to screen readers', async () => {
		setup();
		await tick();
		expect(screen.getByRole('alert').textContent).toBe('Letra F');
	});

	it('announces the letter in English', async () => {
		i18n.set('en');
		setup({ lang: 'en' });
		await tick();
		expect(screen.getByRole('alert').textContent).toBe('Letter F');
		expect(screen.getByRole('img', { name: 'Letter F' })).toBeTruthy();
	});

	it('plays the big reveal when the letter was just revealed, without blocking the fields', async () => {
		const session = new FakeSession(makePlayingView());
		session.frozenNow = NOW;
		session.emit({ type: 'letter_revealed', letter: 'F', round: 2 });
		render(Playing, { session });
		const reveal = screen.getByTestId('letter-reveal');
		expect(reveal.className).toContain('pointer-events-none');
		expect(reveal.getAttribute('aria-hidden')).toBe('true');
		expect(input('Nombre').disabled).toBe(false);
		await vi.advanceTimersByTimeAsync(1200);
		await tick();
		expect(screen.queryByTestId('letter-reveal')).toBeNull();
	});

	it('does not replay the reveal after a reload (no recent event)', () => {
		setup();
		expect(screen.queryByTestId('letter-reveal')).toBeNull();
	});

	it('ignores a letter_revealed event of another round', () => {
		const session = new FakeSession(makePlayingView());
		session.emit({ type: 'letter_revealed', letter: 'B', round: 1 });
		render(Playing, { session });
		expect(screen.queryByTestId('letter-reveal')).toBeNull();
	});
});

describe('Playing: grace and the Basta overlay', () => {
	const grace = (extra: Partial<RoundView> = {}, s: Setup = {}) =>
		setup({
			phase: 'grace',
			mine: ['Fede', 'Francia', ''],
			round: { callerId: 'p2', graceEndsAt: iso(3000), ...extra },
			...s
		});

	it('opens with a full-screen splash with the caller and the Basta sentence, announced assertively', async () => {
		grace();
		await tick();
		const splash = screen.getByTestId('basta-splash');
		expect(splash.textContent).toContain('Beto');
		expect(splash.textContent).toContain('¡Basta para mí, basta para todos!');
		expect(screen.getByRole('alert').textContent).toBe('Beto: ¡Basta para mí, basta para todos!');
		expect(screen.getByRole('alert').getAttribute('aria-live')).toBe('assertive');
	});

	it('vibrates when available and survives a browser without vibration', async () => {
		const vibrate = vi.fn(() => true);
		Object.defineProperty(navigator, 'vibrate', { value: vibrate, configurable: true });
		grace();
		expect(vibrate).toHaveBeenCalledWith([200, 100, 200]);
		Reflect.deleteProperty(navigator, 'vibrate');
		expect(() => grace()).not.toThrow();
	});

	it('survives a vibrate that throws', () => {
		Object.defineProperty(navigator, 'vibrate', {
			value: () => {
				throw new Error('blocked');
			},
			configurable: true
		});
		expect(() => grace()).not.toThrow();
	});

	it('splashes only when the grace starts while the screen is open', async () => {
		const { session } = setup({ mine: ['Fede', 'Francia', 'Foca'] });
		expect(screen.queryByTestId('basta-splash')).toBeNull();
		session.setView(
			makePlayingView({
				phase: 'grace',
				mine: ['Fede', 'Francia', 'Foca'],
				round: { callerId: 'p2', graceEndsAt: iso(3000) }
			})
		);
		await tick();
		expect(screen.getByTestId('basta-splash')).toBeTruthy();
	});

	it('collapses into a banner after 900 ms and the fields stay editable', async () => {
		const { session } = grace();
		expect(screen.getByTestId('basta-splash')).toBeTruthy();
		await advance(session, 900);
		expect(screen.queryByTestId('basta-splash')).toBeNull();
		const banner = screen.getByTestId('grace-banner');
		expect(banner.textContent).toContain('Beto cantó ¡BASTA!');
		expect(input('Animal').disabled).toBe(false);
		await type('Animal', 'Foca');
		expect(input('Animal').value).toBe('Foca');
	});

	it('the splash can be dismissed with a tap', async () => {
		grace();
		await fireEvent.click(screen.getByTestId('basta-splash'));
		expect(screen.queryByTestId('basta-splash')).toBeNull();
	});

	it('does not replay the splash when the screen opens late in the grace (reload)', () => {
		grace({ graceEndsAt: iso(1500) });
		expect(screen.queryByTestId('basta-splash')).toBeNull();
		expect(screen.getByTestId('grace-banner')).toBeTruthy();
	});

	it('counts 3, 2, 1 from graceEndsAt and locks the inputs at 0', async () => {
		const { session } = grace();
		const countdown = () =>
			screen.getByRole('timer', { name: 'Cuenta atrás para cerrar las respuestas' });
		expect(countdown().textContent?.trim()).toBe('3');
		await advance(session, 1000);
		expect(countdown().textContent?.trim()).toBe('2');
		expect(input('Animal').disabled).toBe(false);
		await advance(session, 1000);
		expect(countdown().textContent?.trim()).toBe('1');
		expect(input('Animal').disabled).toBe(false);
		await advance(session, 1000);
		expect(countdown().textContent).toContain('0');
		expect(screen.getByText('Respuestas cerradas')).toBeTruthy();
		for (const label of LABELS.es) expect(input(label).disabled).toBe(true);
	});

	it('computes the countdown from the server clock, not from when the banner appeared', () => {
		grace({ graceEndsAt: iso(1800) });
		expect(screen.getByRole('timer', { name: /cuenta atrás/i }).textContent?.trim()).toBe('2');
	});

	it('tells the caller it was them', async () => {
		const { session } = grace({ callerId: 'p1' });
		await advance(session, 900);
		expect(screen.getByTestId('grace-banner').textContent).toContain('¡Cantaste BASTA!');
	});

	it('shows "Se acabó el tiempo" instead of a caller when the time limit expired', async () => {
		const { session } = grace({ callerId: '', graceEndsAt: iso(-50), timeLimitEndsAt: iso(-50) });
		const banner = screen.getByTestId('grace-banner');
		expect(banner.textContent).toContain('¡Se acabó el tiempo!');
		expect(banner.textContent).not.toContain('cantó');
		for (const label of LABELS.es) expect(input(label).disabled).toBe(true);
		await advance(session, 10);
		expect(banner.textContent).toContain('¡Se acabó el tiempo!');
	});

	it('announces the time-up on the splash when it happens with the screen open', async () => {
		const { session } = setup({ mine: ['Fede', '', ''], round: { timeLimitEndsAt: iso(5000) } });
		await type('País', 'Francia');
		session.setView(
			makePlayingView({
				phase: 'grace',
				mine: ['Fede', '', ''],
				round: { callerId: '', timeLimitEndsAt: iso(5000), graceEndsAt: iso(0) }
			})
		);
		await tick();
		expect(screen.getByTestId('basta-splash').textContent).toContain('¡Se acabó el tiempo!');
		expect(screen.getByRole('alert').textContent).toBe('¡Se acabó el tiempo!');
		expect(sent(session)).toEqual([['setAnswer', 1, 'Francia']]);
	});

	it('spectators also see the splash and the countdown banner, with no fields', async () => {
		const { session } = grace(
			{},
			{ players: [...makePlayers(), player('p5', 'Eli')], youId: 'p5' }
		);
		expect(screen.getByTestId('basta-splash').textContent).toContain('Beto');
		await advance(session, 900);
		expect(screen.getByTestId('grace-banner').textContent).toContain('Beto cantó ¡BASTA!');
		expect(screen.queryAllByRole('textbox')).toHaveLength(0);
	});

	it('in English the splash says only "Stop!"', async () => {
		i18n.set('en');
		grace({}, { lang: 'en' });
		await tick();
		const splash = screen.getByTestId('basta-splash');
		expect(splash.textContent?.replace(/\p{Extended_Pictographic}|\s+/gu, ' ').trim()).toBe(
			'Beto Stop!'
		);
		expect(screen.getByRole('alert').textContent).toBe('Beto: Stop!');
		expect(screen.getByTestId('grace-banner').textContent).toContain('Beto called Stop!');
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});

	it('in English the time-up banner and splash have no Spanish', async () => {
		i18n.set('en');
		setup({
			phase: 'grace',
			lang: 'en',
			round: { callerId: '', graceEndsAt: iso(-50), timeLimitEndsAt: iso(-50) }
		});
		expect(screen.getByTestId('grace-banner').textContent).toContain("Time's up!");
		expect(screen.getByTestId('grace-banner').textContent).toContain('Answers locked');
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});

describe('Playing: English', () => {
	it('uses Stop! for the button and English hints', async () => {
		i18n.set('en');
		setup({ lang: 'en', mine: ['Fred', '', 'Fox'] });
		const button = bastaButton() as HTMLButtonElement;
		expect(button.textContent?.trim()).toBe('Stop!');
		expect(button.disabled).toBe(true);
		expect(screen.getByText('Fill in every category')).toBeTruthy();
		await type('Country', 'Brazil');
		expect(screen.getByText(/must start with F/i)).toBeTruthy();
		expect(screen.getByText('Check the letter in: Country')).toBeTruthy();
		expect(screen.getByText('Round 2 of 5')).toBeTruthy();
		expect(document.body.textContent).not.toMatch(/[¡¿ñáéíóúü]/i);
	});
});

describe('Playing: screen wake lock', () => {
	it('holds a screen wake lock while mounted and releases it afterwards', async () => {
		const release = vi.fn(async () => {});
		const request = vi.fn(async () => ({ release }));
		vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { wakeLock: { request } }));
		const { unmount } = setup();
		await vi.advanceTimersByTimeAsync(0);
		expect(request).toHaveBeenCalledWith('screen');
		unmount();
		expect(release).toHaveBeenCalledTimes(1);
	});

	it('works without the Wake Lock API', () => {
		expect(() => setup()).not.toThrow();
	});
});
