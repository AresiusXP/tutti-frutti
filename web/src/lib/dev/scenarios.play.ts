import { FakeSession } from '$lib/game/fake.svelte';
import { makeRound, makeSettings, makeView, player } from '$lib/game/fixtures';
import type { Phase, Player, RoundView, View } from '$lib/protocol';
import type { Scenario } from './scenarios';

/**
 * Scenarios of the Letter and Playing screens (`/dev?s=play-...`). Every
 * scenario exists three times: `play-<name>` (Spanish lobby), `play-en-<name>`
 * (English lobby: English category labels, ñ folds to n) and `play-nl-<name>`
 * (Dutch lobby: the catalog's Dutch labels, no Ñ). The UI language is
 * independent: add `&lang=en` or `&lang=nl`.
 *
 * Clocks are frozen so screenshots are stable; the `live-*` scenarios use the
 * real clock to watch the countdowns run.
 */
type Lang = 'es' | 'en' | 'nl';

const NOW = Date.parse('2026-05-01T12:00:00Z');
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

const LABELS: Record<Lang, string[]> = {
	es: [
		'Nombre',
		'País',
		'Animal',
		'Comida',
		'Color',
		'Marca',
		'Ciudad',
		'Profesión',
		'Deporte',
		'Fruta'
	],
	en: ['Name', 'Country', 'Animal', 'Food', 'Color', 'Brand', 'City', 'Job', 'Sport', 'Fruit'],
	nl: ['Naam', 'Land', 'Dier', 'Eten', 'Kleur', 'Merk', 'Stad', 'Beroep', 'Sport', 'Fruit']
};
const IDS = [
	'nombre',
	'pais',
	'animal',
	'comida',
	'color',
	'marca',
	'ciudad',
	'profesion',
	'deporte',
	'fruta'
];

/** Answers that all start with F. */
const GOOD: Record<Lang, string[]> = {
	es: [
		'Fede',
		'Francia',
		'Foca',
		'Fideos',
		'Fucsia',
		'Ford',
		'Florencia',
		'Fotógrafo',
		'Fútbol',
		'Frutilla'
	],
	en: [
		'Fred',
		'France',
		'Fox',
		'Fries',
		'Fuchsia',
		'Ford',
		'Florence',
		'Farmer',
		'Football',
		'Fig'
	],
	nl: [
		'Femke',
		'Frankrijk',
		'Flamingo',
		'Friet',
		'Fuchsia',
		'Ford',
		'Firenze',
		'Fotograaf',
		'Fietsen',
		'Framboos'
	]
};

/** An answer that does not start with F: [country, colour] per lobby language. */
const WRONG_LETTER: Record<Lang, [string, string]> = {
	es: ['Brasil', 'Azul'],
	en: ['Brazil', 'Blue'],
	nl: ['Brazilië', 'Blauw']
};

/** Dutch answers for the letter I: the digraph IJ, also typed as the ligature Ĳ (equal for the server). */
const IJ = ['Iris', 'Ĳsland', 'IJsbeer', 'IJs', 'Indigo', 'Ikea'];

const AVATARS = [
	{ emoji: '🍌', color: 'banana' },
	{ emoji: '🍓', color: 'strawberry' },
	{ emoji: '🍇', color: 'grape' },
	{ emoji: '🍊', color: 'orange' },
	{ emoji: '🍍', color: 'lime' },
	{ emoji: '🍉', color: 'blueberry' },
	{ emoji: '🍌', color: 'strawberry' },
	{ emoji: '🍓', color: 'lime' },
	{ emoji: '🍇', color: 'orange' },
	{ emoji: '🍊', color: 'blueberry' }
];
const NAMES = ['Ana', 'Beto', 'Caro', 'Dani', 'Eli', 'Fabi', 'Gus', 'Hugo', 'Inés', 'Jorge'];

function roster(count: number, over: Record<string, Partial<Player>> = {}): Player[] {
	return Array.from({ length: count }, (_, i) =>
		player(`p${i + 1}`, NAMES[i], { avatar: AVATARS[i], ...over[`p${i + 1}`] })
	);
}

interface Setup {
	lang: Lang;
	phase: Phase;
	players?: Player[];
	youId?: string;
	categories?: number;
	rounds?: number;
	timeLimitSeconds?: number;
	chooserId?: string;
	usedLetters?: string[];
	roundNum?: number;
	round?: Partial<RoundView>;
	/** The viewer's own answers (index-aligned with the categories). */
	mine?: string[];
	status?: FakeSession['status'];
	frozen?: boolean;
}

function build(setup: Setup): FakeSession {
	const {
		lang,
		phase,
		players = roster(5, { p3: { connected: false } }),
		youId = 'p1',
		categories = 6,
		rounds = 5,
		timeLimitSeconds = 0,
		chooserId = 'p2',
		usedLetters = ['A', 'M'],
		roundNum = 3
	} = setup;
	const settings = makeSettings({
		lang,
		rounds,
		timeLimitSeconds,
		categories: IDS.slice(0, categories).map((id) => ({ id }))
	});
	const participants = setup.round?.participants ?? players.slice(0, 5).map((p) => p.id);
	const hasLetter = phase !== 'letter';
	const answers = { ...(setup.round?.answers ?? {}) };
	const filled = { p2: 3, p3: 6, p4: 1, p5: 0, ...(setup.round?.filled ?? {}) };
	if (setup.mine) answers[youId] = setup.mine;
	const view: View = makeView({
		phase,
		players,
		youId,
		hostId: 'p1',
		settings,
		categoryLabels: LABELS[lang].slice(0, categories),
		usedLetters: hasLetter ? [...usedLetters, 'F'] : usedLetters,
		roundNum,
		chooserId,
		round: makeRound({
			number: roundNum,
			letter: hasLetter ? 'F' : '',
			...setup.round,
			participants,
			answers,
			filled
		})
	});
	const session = new FakeSession(view);
	if (setup.frozen !== false) session.frozenNow = NOW;
	if (setup.status) session.status = setup.status;
	return session;
}

type Def = (lang: Lang) => FakeSession;

/** Dev only: lets a browser script read `session.sent` (`window.__tfSession`). */
function expose(session: FakeSession): FakeSession {
	if (typeof window !== 'undefined') Object.assign(window, { __tfSession: session });
	return session;
}

const half = (lang: Lang) => [GOOD[lang][0], GOOD[lang][1], GOOD[lang][2], '', '', ''];

const defs: Record<string, Def> = {
	'letter-chooser': (lang) => build({ lang, phase: 'letter', youId: 'p1', chooserId: 'p1' }),
	'letter-waiting': (lang) => build({ lang, phase: 'letter', youId: 'p1', chooserId: 'p2' }),
	'letter-first-round': (lang) =>
		build({ lang, phase: 'letter', chooserId: 'p2', usedLetters: [], roundNum: 1 }),
	'letter-no-limit': (lang) => build({ lang, phase: 'letter', chooserId: 'p2', rounds: 0 }),
	'letter-nonparticipant': (lang) =>
		build({
			lang,
			phase: 'letter',
			players: roster(6),
			youId: 'p6',
			chooserId: 'p2',
			round: { participants: ['p1', 'p2', 'p3', 'p4', 'p5'] }
		}),

	'playing-empty': (lang) => build({ lang, phase: 'playing' }),
	'playing-half': (lang) => build({ lang, phase: 'playing', mine: half(lang) }),
	'playing-valid': (lang) =>
		build({
			lang,
			phase: 'playing',
			mine: GOOD[lang].slice(0, 6),
			round: { filled: { p2: 6, p3: 6, p4: 4, p5: 2 } }
		}),
	'playing-wrong-letter': (lang) =>
		build({
			lang,
			phase: 'playing',
			mine: [
				GOOD[lang][0],
				WRONG_LETTER[lang][0],
				GOOD[lang][2],
				GOOD[lang][3],
				WRONG_LETTER[lang][1],
				GOOD[lang][5]
			]
		}),
	'playing-enye': (lang) =>
		build({
			lang,
			phase: 'playing',
			mine: ['Ñandú', 'Ñapa', 'Ñu', 'Nube', 'Niño', 'Nike'],
			round: { letter: 'Ñ' }
		}),
	'playing-ij': (lang) => build({ lang, phase: 'playing', mine: IJ, round: { letter: 'I' } }),
	'playing-with-timer': (lang) =>
		build({
			lang,
			phase: 'playing',
			timeLimitSeconds: 120,
			mine: half(lang),
			round: { timeLimitEndsAt: iso(83_000) }
		}),
	'playing-timer-warning': (lang) =>
		build({
			lang,
			phase: 'playing',
			timeLimitSeconds: 60,
			mine: half(lang),
			round: { timeLimitEndsAt: iso(8_000) }
		}),
	'playing-timer-last-seconds': (lang) =>
		build({
			lang,
			phase: 'playing',
			timeLimitSeconds: 60,
			mine: GOOD[lang].slice(0, 6),
			round: { timeLimitEndsAt: iso(3_200) }
		}),
	'playing-full': (lang) =>
		build({
			lang,
			phase: 'playing',
			players: roster(10, { p3: { connected: false }, p7: { connected: false } }),
			categories: 10,
			timeLimitSeconds: 90,
			mine: GOOD[lang].slice(0, 7).concat(['', '', '']),
			round: {
				timeLimitEndsAt: iso(47_000),
				participants: roster(10).map((p) => p.id),
				filled: { p2: 10, p3: 4, p4: 7, p5: 0, p6: 9, p7: 2, p8: 10, p9: 5, p10: 1 }
			}
		}),
	'playing-reconnecting': (lang) =>
		build({ lang, phase: 'playing', mine: half(lang), status: 'reconnecting' }),
	spectator: (lang) =>
		build({
			lang,
			phase: 'playing',
			players: roster(6),
			youId: 'p6',
			timeLimitSeconds: 120,
			round: {
				timeLimitEndsAt: iso(83_000),
				participants: ['p1', 'p2', 'p3', 'p4', 'p5'],
				filled: { p1: 2, p2: 3, p3: 6, p4: 1, p5: 0 }
			}
		}),

	// The caller's grace, 2 s left: straight to the banner (the splash only plays in the first 0.9 s).
	'grace-basta': (lang) =>
		build({
			lang,
			phase: 'grace',
			mine: GOOD[lang].slice(0, 6),
			round: { callerId: 'p2', graceEndsAt: iso(2_000), filled: { p2: 6, p3: 6, p4: 6, p5: 5 } }
		}),
	'grace-basta-by-me': (lang) =>
		build({
			lang,
			phase: 'grace',
			mine: GOOD[lang].slice(0, 6),
			round: { callerId: 'p1', graceEndsAt: iso(1_000) }
		}),
	// Just called: the full-screen splash is visible for the first 0.9 s.
	'grace-splash': (lang) =>
		build({
			lang,
			phase: 'grace',
			mine: half(lang),
			round: { callerId: 'p2', graceEndsAt: iso(3_000) }
		}),
	'grace-locked': (lang) =>
		build({
			lang,
			phase: 'grace',
			mine: half(lang),
			round: { callerId: 'p2', graceEndsAt: iso(-100) }
		}),
	'grace-time-up': (lang) =>
		build({
			lang,
			phase: 'grace',
			timeLimitSeconds: 60,
			mine: half(lang),
			round: { callerId: '', timeLimitEndsAt: iso(-100), graceEndsAt: iso(-100) }
		}),
	'grace-spectator': (lang) =>
		build({
			lang,
			phase: 'grace',
			players: roster(6),
			youId: 'p6',
			round: {
				callerId: 'p2',
				graceEndsAt: iso(2_000),
				participants: ['p1', 'p2', 'p3', 'p4', 'p5'],
				filled: { p1: 3, p2: 6, p3: 6, p4: 1, p5: 0 }
			}
		}),

	// The letter was just revealed: plays the big pop-in (real clock, replays on every load).
	reveal: (lang) => {
		const session = build({ lang, phase: 'playing', frozen: false });
		session.emit({ type: 'letter_revealed', letter: 'F', round: 3 });
		return session;
	},
	'live-timer': (lang) => {
		const session = build({ lang, phase: 'playing', frozen: false, timeLimitSeconds: 30 });
		session.view!.round!.timeLimitEndsAt = new Date(Date.now() + 30_000).toISOString();
		return session;
	},
	'live-grace': (lang) => {
		const session = build({ lang, phase: 'grace', frozen: false, mine: half(lang) });
		session.view!.round!.callerId = 'p2';
		session.view!.round!.graceEndsAt = new Date(Date.now() + 3_000).toISOString();
		return session;
	}
};

export const scenarios: Record<string, Scenario> = {};
for (const [name, def] of Object.entries(defs)) {
	scenarios[`play-${name}`] = () => expose(def('es'));
	scenarios[`play-en-${name}`] = () => expose(def('en'));
	scenarios[`play-nl-${name}`] = () => expose(def('nl'));
}
