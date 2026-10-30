import { describe, expect, it } from 'vitest';
import * as protocol from '$lib/protocol';
import { errorMessage, KNOWN_ERROR_CODES } from './errors';
import { i18n } from './i18n.svelte';
import { m } from './paraglide/messages';

/** Every `Code*` (engine) and `Err*` (protocol) constant the server can send. */
const serverCodes = Object.entries(protocol)
	.filter(([name, value]) => /^(Code|Err)[A-Z]/.test(name) && typeof value === 'string')
	.map(([, value]) => value as string);

describe('errorMessage', () => {
	it('covers every engine code and every protocol error code', () => {
		expect(serverCodes.length).toBeGreaterThanOrEqual(34);
		for (const code of serverCodes) {
			expect(KNOWN_ERROR_CODES, code).toContain(code);
		}
	});

	it('also explains not_connected and the REST client codes', () => {
		for (const code of ['not_connected', 'network', 'forbidden_origin', 'invalid_lang']) {
			expect(KNOWN_ERROR_CODES).toContain(code);
		}
	});

	it('never falls back to the generic message for a known code, in both languages', () => {
		for (const locale of ['es', 'en', 'nl']) {
			i18n.set(locale);
			for (const code of KNOWN_ERROR_CODES) {
				const text = errorMessage(code);
				expect(text, `${locale}.${code}`).not.toBe('');
				expect(text, `${locale}.${code}`).not.toBe(m.err_unknown());
			}
		}
		i18n.set('es');
	});

	it('explains the four vote-to-stop codes in every language, never with the generic sentence', () => {
		const codes = ['stop_vote_disabled', 'stop_vote_pending', 'no_stop_vote', 'stop_not_needed'];
		for (const locale of ['es', 'en', 'nl']) {
			i18n.set(locale);
			const texts = codes.map((code) => errorMessage(code));
			expect(new Set(texts).size, locale).toBe(codes.length);
			for (const text of texts) expect(text, locale).not.toBe(m.err_unknown());
			// Stop! in en/nl, ¡BASTA! in es: never the Spanish word on another screen
			if (locale !== 'es')
				for (const text of texts) expect(text, locale).not.toMatch(/basta|[¡¿ñ]/i);
		}
		i18n.set('en');
		expect(errorMessage('stop_not_needed')).toContain('Stop!');
		i18n.set('nl');
		expect(errorMessage('stop_not_needed')).toContain('Stop!');
		i18n.set('es');
		expect(errorMessage('stop_not_needed')).toContain('¡BASTA!');
	});

	it('explains the AI judge codes in every language, never with the generic sentence', () => {
		const codes = [
			'judge_pending',
			'already_judged',
			'nothing_to_judge',
			'judge_unavailable',
			'login_required'
		];
		for (const locale of ['es', 'en', 'nl']) {
			i18n.set(locale);
			const texts = codes.map((code) => errorMessage(code));
			expect(new Set(texts).size, locale).toBe(codes.length);
			for (const text of texts) expect(text, locale).not.toBe(m.err_unknown());
		}
		i18n.set('es');
	});

	it('explains the content-safety codes in every language, each differently, never with the generic sentence', () => {
		const codes = [
			'terms_required',
			'terms_version_mismatch',
			'age_confirmation_required',
			'invalid_locale',
			'invalid_report',
			'not_in_room',
			'invalid_block',
			'block_limit',
			'join_unavailable'
		];
		for (const locale of ['es', 'en', 'nl']) {
			i18n.set(locale);
			const texts = codes.map((code) => errorMessage(code));
			// terms_version_mismatch deliberately says the same as the gate's "updated" notice
			expect(new Set(texts).size, locale).toBe(codes.length);
			for (const text of texts) expect(text, locale).not.toBe(m.err_unknown());
		}
		i18n.set('es');
	});

	it('join_unavailable never says who or why (a block, a restriction): one vague sentence', () => {
		const texts: Record<string, string> = {};
		for (const locale of ['es', 'en', 'nl']) {
			i18n.set(locale);
			texts[locale] = errorMessage('join_unavailable');
			expect(texts[locale], locale).not.toMatch(
				/block|bloque|blokk|restrict|ban|prohib|verbod|report|meld|jugador|player|speler/i
			);
		}
		expect(texts.en).toBe("You can't join this game right now.");
		i18n.set('es');
	});

	it('falls back to err_unknown for anything else and follows the language', () => {
		i18n.set('es');
		expect(errorMessage('definitely_new')).toBe(m.err_unknown());
		expect(errorMessage('not_host')).toBe('Solo el anfitrión puede hacer eso.');
		i18n.set('en');
		expect(errorMessage('not_host')).toBe('Only the host can do that.');
		expect(errorMessage('basta_not_allowed')).toContain('Stop!');
		i18n.set('es');
	});
});
