import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';
import { m } from './paraglide/messages';
import { REPORT_REASONS } from './safety/targets';

type Catalog = Record<string, string>;

/** Message areas: the shared catalog plus one folder per feature (each owned by one part of the app). */
const AREAS = ['', 'net/', 'lobby/', 'play/', 'review/', 'account/', 'support/', 'pwa/', 'safety/'];

function readArea(area: string, locale: string): Catalog {
	const raw = JSON.parse(
		readFileSync(new URL(`../../messages/${area}${locale}.json`, import.meta.url), 'utf8')
	) as Catalog;
	return Object.fromEntries(Object.entries(raw).filter(([key]) => !key.startsWith('$')));
}

function load(locale: string): Catalog {
	const merged: Catalog = {};
	for (const area of AREAS) {
		for (const [key, value] of Object.entries(readArea(area, locale))) {
			if (key in merged)
				throw new Error(`duplicate message key "${key}" (${area || 'core'}/${locale})`);
			merged[key] = value;
		}
	}
	return merged;
}

const es = load('es');
const en = load('en');
const nl = load('nl');
const catalogs = { es, en, nl };
const params = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort();

describe('message catalogs', () => {
	it('every area defines exactly the same keys in es, en and nl', () => {
		for (const area of AREAS) {
			const keys = Object.keys(readArea(area, 'es')).sort();
			expect(Object.keys(readArea(area, 'en')).sort(), `${area || 'core'}/en`).toEqual(keys);
			expect(Object.keys(readArea(area, 'nl')).sort(), `${area || 'core'}/nl`).toEqual(keys);
		}
		expect(Object.keys(en).sort()).toEqual(Object.keys(es).sort());
		expect(Object.keys(nl).sort()).toEqual(Object.keys(es).sort());
	});

	it('uses the same {parameters} in every language', () => {
		for (const key of Object.keys(es)) {
			expect(params(en[key]), `en.${key}`).toEqual(params(es[key]));
			expect(params(nl[key]), `nl.${key}`).toEqual(params(es[key]));
		}
	});

	it('has no empty or untrimmed message', () => {
		for (const [locale, catalog] of Object.entries(catalogs)) {
			for (const [key, value] of Object.entries(catalog)) {
				expect(value.trim(), `${locale}.${key}`).not.toBe('');
				expect(value, `${locale}.${key}`).toBe(value.trim());
			}
		}
	});

	it('never contains the brand as literal text', () => {
		for (const [locale, catalog] of Object.entries(catalogs)) {
			for (const [key, value] of Object.entries(catalog)) {
				expect(value.toLowerCase(), `${locale}.${key}`).not.toContain(BRAND.toLowerCase());
				expect(value.toLowerCase().replace(/\s/g, ''), `${locale}.${key}`).not.toContain(
					BRAND.toLowerCase().replace(/\s/g, '')
				);
			}
		}
	});

	it('passes the brand via {brand} where a message needs it', () => {
		for (const catalog of Object.values(catalogs)) {
			expect(params(catalog.share_text)).toEqual(['brand', 'code']);
		}
		expect(m.share_text({ brand: BRAND, code: 'KT7P' }, { locale: 'es' })).toBe(
			'¡Jugá Tutti Frutti conmigo! Código KT7P'
		);
		expect(m.share_text({ brand: BRAND, code: 'KT7P' }, { locale: 'en' })).toBe(
			'Play Tutti Frutti with me! Code KT7P'
		);
		expect(m.share_text({ brand: BRAND, code: 'KT7P' }, { locale: 'nl' })).toBe(
			'Speel Tutti Frutti met me mee! Code KT7P'
		);
	});

	it('keeps the BRAND constant and the static HTML shell in sync', () => {
		expect(BRAND).toBe('Tutti Frutti');
		const html = readFileSync(new URL('../app.html', import.meta.url), 'utf8');
		expect(html).toContain(`<title>${BRAND}</title>`);
	});

	it('compiles every key for every locale (no missing translation falls back to Spanish)', () => {
		for (const locale of ['en', 'nl'] as const) {
			for (const key of ['basta_button', 'create_game', 'lobby_lang_nl', 'language_label']) {
				const fn = (m as unknown as Record<string, (i: object, o: object) => string>)[key];
				expect(fn({}, { locale }), `${locale}.${key}`).toBe(catalogs[locale][key]);
			}
		}
	});
});

describe('Basta / Stop wording', () => {
	it('English and Dutch say exactly "Stop!" with no Spanish text', () => {
		for (const locale of ['en', 'nl'] as const) {
			const catalog = catalogs[locale];
			expect(catalog.basta_button, locale).toBe('Stop!');
			expect(catalog.basta_overlay, locale).toBe('Stop!');
			expect(catalog.letter_stop, locale).toBe('Stop!');
			expect(m.basta_button({}, { locale }), locale).toBe('Stop!');
			expect(m.basta_overlay({}, { locale }), locale).toBe('Stop!');
			expect(m.letter_stop({}, { locale }), locale).toBe('Stop!');
		}
	});

	it('Spanish uses ¡BASTA! and the full overlay sentence', () => {
		expect(es.basta_button).toBe('¡BASTA!');
		expect(es.basta_overlay).toBe('¡Basta para mí, basta para todos!');
		expect(es.letter_stop).toBe('¡STOP!');
		expect(m.basta_button({}, { locale: 'es' })).toBe('¡BASTA!');
	});

	it('English strings contain no Spanish punctuation or accents', () => {
		for (const [key, value] of Object.entries(en)) {
			expect(value, key).not.toMatch(/[¡¿ñáéíóúü]/i);
		}
	});

	it('Dutch strings contain no Spanish punctuation, no ñ and never the word "basta"', () => {
		for (const [key, value] of Object.entries(nl)) {
			expect(value, `nl.${key}`).not.toMatch(/[¡¿ñÑ]/);
		}
	});

	it('English and Dutch never say "basta"', () => {
		for (const locale of ['en', 'nl'] as const) {
			for (const [key, value] of Object.entries(catalogs[locale])) {
				expect(value, `${locale}.${key}`).not.toMatch(/basta/i);
			}
		}
	});

	it('Spanish uses voseo', () => {
		expect(es.create_game).toMatch(/^Creá/);
		expect(es.join_game).toMatch(/^Unite/);
	});
});

describe('Dutch wording', () => {
	it('uses the shared glossary', () => {
		expect(nl.account_sign_in_google).toBe('Inloggen met Google');
		expect(nl.language_label).toBe('Taal');
		expect(nl.lobby_lang_nl).toBe('Nederlands');
		expect(nl.lobby_lang_es).toBe('Spaans');
		expect(nl.lobby_lang_en).toBe('Engels');
	});

	it('speaks informally (je/jij), never formally (u)', () => {
		for (const [key, value] of Object.entries(nl)) {
			expect(value, `nl.${key}`).not.toMatch(/\b(u|uw)\b/i);
		}
	});

	it('leaves no untranslated Spanish or English copy', () => {
		// Values that are legitimately spelled the same in Dutch (loanwords, symbols, "Stop!").
		const SAME = new Set([
			'basta_button',
			'basta_overlay',
			'letter_stop',
			'lobby_qr',
			'lobby_host_badge',
			'lobby_offline',
			'lobby_time_secs',
			'lobby_group_count',
			'lobby_custom_counter',
			'play_letter_announce',
			'play_letter_name',
			'review_category_pos',
			'review_letter',
			'review_offline',
			'final_podium',
			'final_home'
		]);
		for (const key of Object.keys(nl)) {
			const copied = nl[key] === en[key] || nl[key] === es[key];
			expect(!copied || SAME.has(key), `nl.${key} is a copy of the English/Spanish text`).toBe(
				true
			);
		}
	});
});

describe('vote to stop wording', () => {
	const keys = Object.keys(readArea('play/', 'es')).filter((key) =>
		/^play_(ask|vote|stop|hint_ask)_/.test(key)
	);
	const lobbyKeys = Object.keys(readArea('lobby/', 'es')).filter((key) =>
		/^lobby_(stop|summary_stop)_/.test(key)
	);
	const errorKeys = [
		'err_stop_vote_disabled',
		'err_stop_vote_pending',
		'err_no_stop_vote',
		'err_stop_not_needed'
	];

	it('exists in the right area folders for all three languages', () => {
		expect(keys).toHaveLength(22);
		expect(lobbyKeys).toHaveLength(5);
		for (const locale of ['es', 'en', 'nl'] as const) {
			for (const key of keys)
				expect(readArea('play/', locale), `${locale}.${key}`).toHaveProperty(key);
			for (const key of lobbyKeys)
				expect(readArea('lobby/', locale), `${locale}.${key}`).toHaveProperty(key);
			for (const key of errorKeys)
				expect(readArea('net/', locale), `${locale}.${key}`).toHaveProperty(key);
		}
	});

	it('calls it "pedir que paren", "ask everyone to stop" and "vraag iedereen om te stoppen"', () => {
		expect(es.play_ask_title).toBe('¿Pedir que paren?');
		expect(es.lobby_stop_label).toBe('Permitir pedir que paren');
		expect(es.play_hint_ask_many).toContain('pedir que paren');
		expect(en.play_ask_title).toBe('Ask everyone to stop?');
		expect(en.play_hint_ask_many).toContain('ask everyone to stop');
		expect(nl.play_ask_title).toBe('Vraag iedereen om te stoppen?');
		expect(nl.play_hint_ask_many).toContain('iedereen te vragen om te stoppen');
	});

	it('only Spanish says ¡BASTA!; English and Dutch say Stop! and nothing Spanish', () => {
		for (const key of [...keys, ...lobbyKeys, ...errorKeys]) {
			expect(en[key], `en.${key}`).not.toMatch(/[¡¿ñáéíóúü]|basta/i);
			expect(nl[key], `nl.${key}`).not.toMatch(/[¡¿ñ]|basta/i);
		}
		expect(es.lobby_stop_off).toContain('¡BASTA!');
		expect(en.lobby_stop_off).toContain('Stop!');
		expect(nl.lobby_stop_off).toContain('Stop!');
	});

	it('stays short enough for a 360 px phone (hints and buttons)', () => {
		for (const catalog of [es, en, nl]) {
			for (const key of ['play_vote_yes', 'play_vote_no', 'play_ask_confirm', 'play_ask_keep']) {
				expect(catalog[key].length, key).toBeLessThanOrEqual(20);
			}
			for (const key of ['play_hint_ask_one', 'play_hint_ask_many']) {
				expect(catalog[key].length, key).toBeLessThanOrEqual(60);
			}
		}
	});
});

describe('safety wording', () => {
	const keysOf = (prefix: RegExp) =>
		Object.keys(readArea('safety/', 'es')).filter((key) => prefix.test(key));

	it('lives in its own area folder in all three languages', () => {
		for (const locale of ['es', 'en', 'nl'] as const) {
			const area = readArea('safety/', locale);
			for (const key of [
				'terms_gate_title',
				'terms_gate_age_label',
				'report_send',
				'report_sent',
				'block_confirm',
				'blocked_title',
				'err_terms_required',
				'err_join_unavailable'
			]) {
				expect(area, `${locale}.${key}`).toHaveProperty(key);
			}
		}
		// one message per reason the server accepts (the protocol constants), plus the legend and the "choose one" note
		for (const reason of REPORT_REASONS) {
			expect(keysOf(/^report_reason_/), reason).toContain(`report_reason_${reason}`);
		}
		expect(keysOf(/^report_reason_/)).toHaveLength(REPORT_REASONS.length + 2);
	});

	it('the terms box says what the server records: 13 or older and accepting the terms', () => {
		expect(en.terms_gate_age_label).toBe('I am 13 or older and accept the Terms');
		expect(es.terms_gate_age_label).toMatch(/13 años o más/);
		expect(nl.terms_gate_age_label).toMatch(/13 jaar of ouder/);
	});

	it('"Block and leave" explains the four things the owner promised, in every language', () => {
		const facts: Record<string, RegExp[]> = {
			es: [/Salís/, /bloqueado/, /cookies/, /no recibe/],
			en: [/leave this game/, /blocked/, /Clearing cookies/, /not told/],
			nl: [/verlaat/, /geblokkeerd/, /Cookies wissen/, /niets van te horen/]
		};
		for (const [locale, catalog] of Object.entries(catalogs)) {
			const text = [
				catalog.block_leave,
				catalog.block_blocked,
				catalog.block_undo,
				catalog.block_silent
			].join(' ');
			for (const fact of facts[locale]) expect(text, locale).toMatch(fact);
		}
	});

	it('the accessible names of the icon buttons are longer than any text they show (WCAG 2.5.3) and say who or what', () => {
		for (const [locale, catalog] of Object.entries(catalogs)) {
			for (const key of [
				'report_player_aria',
				'report_answer_aria',
				'report_proposal_aria',
				'report_label_aria',
				'report_verdict_aria'
			]) {
				const name = catalog[key];
				expect(params(name).length, `${locale}.${key}`).toBe(1);
			}
			// "Desbloquear" is the visible text of the unblock button: its name must start with the same word
			expect(catalog.blocked_unblock_aria.toLowerCase(), locale).toContain(
				catalog.blocked_unblock.toLowerCase()
			);
		}
	});

	it('speaks plain Spanish with voseo, and informal Dutch', () => {
		expect(es.terms_gate_intro).toMatch(/tenés/);
		expect(es.report_reason_required).toMatch(/Elegí/);
		expect(nl.terms_gate_intro).not.toMatch(/\b(u|uw)\b/);
	});

	it('English and Dutch never carry the Spanish call or characters (the generic checks cover every key; this names the area)', () => {
		for (const key of Object.keys(readArea('safety/', 'es'))) {
			expect(en[key], `en.${key}`).not.toMatch(/[¡¿ñáéíóúü]|basta/i);
			expect(nl[key], `nl.${key}`).not.toMatch(/[¡¿ñ]|basta/i);
		}
	});

	it('never names the game in a message (it is the BRAND constant)', () => {
		for (const catalog of Object.values(catalogs)) {
			for (const key of Object.keys(readArea('safety/', 'es'))) {
				expect(catalog[key].toLowerCase()).not.toContain('tutti');
			}
		}
	});

	it('the comment limit is the one of the server (280) and the sheet says it with {max}', () => {
		expect(params(es.report_comment_hint)).toEqual(['max']);
		expect(params(en.report_comment_hint)).toEqual(['max']);
		expect(params(nl.report_comment_hint)).toEqual(['max']);
	});
});
