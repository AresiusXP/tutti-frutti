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
		for (const locale of ['es', 'en']) {
			i18n.set(locale);
			for (const code of KNOWN_ERROR_CODES) {
				const text = errorMessage(code);
				expect(text, `${locale}.${code}`).not.toBe('');
				expect(text, `${locale}.${code}`).not.toBe(m.err_unknown());
			}
		}
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
