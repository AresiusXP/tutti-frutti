// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	detectSupportEnv,
	SUPPORT_ARGENTINA,
	SUPPORT_DEFAULT,
	supportLink,
	supportUrl
} from './support';

describe('supportUrl', () => {
	it.each([
		['Buenos Aires', 'America/Argentina/Buenos_Aires', ['es-AR']],
		['Córdoba (current name)', 'America/Argentina/Cordoba', ['es-AR']],
		['Ushuaia', 'America/Argentina/Ushuaia', ['es-AR']],
		['Córdoba (legacy alias)', 'America/Cordoba', ['es']],
		['Buenos Aires (legacy alias)', 'America/Buenos_Aires', ['en-US']],
		['Mendoza (legacy alias)', 'America/Mendoza', ['nl-NL']],
		['an English screen on an Argentine device', 'America/Argentina/Salta', ['en-US']],
		['no timezone but an Argentine language', undefined, ['es-AR']],
		['a foreign timezone but an Argentine language', 'Europe/Amsterdam', ['es-AR']],
		['the Argentine tag second in the list', 'Europe/Amsterdam', ['nl-NL', 'es-AR']],
		['English as spoken in Argentina', 'Europe/Madrid', ['en-AR']],
		['a script subtag', 'Europe/Madrid', ['es-Latn-AR']],
		['a lower-case tag', 'Europe/Madrid', ['es-ar']],
		['an extended language subtag', 'Europe/Madrid', ['zh-yue-AR']],
		['a unicode extension', 'Europe/Madrid', ['es-AR-u-ca-gregory']],
		['a longer language subtag', 'Europe/Madrid', ['gsw-AR']]
	])('sends %s to Cafecito', (_name, timeZone, languages) => {
		expect(supportUrl({ timeZone, languages })).toBe(SUPPORT_ARGENTINA);
	});

	it.each([
		['the Netherlands', 'Europe/Amsterdam', ['nl-NL']],
		['Belgium', 'Europe/Brussels', ['nl-BE', 'fr-BE']],
		['Spain', 'Europe/Madrid', ['es-ES']],
		['Uruguay (next door)', 'America/Montevideo', ['es-UY']],
		['Chile', 'America/Santiago', ['es-CL']],
		['Mexico', 'America/Mexico_City', ['es-MX']],
		['the US', 'America/New_York', ['en-US']],
		['a Latin American Spanish tag', 'America/Bogota', ['es-419']],
		['a zone that only starts like Argentina', 'America/Argentinaland', ['en-US']],
		['a language that only contains "ar" (Arabic)', 'Asia/Riyadh', ['ar', 'ar-SA']],
		['Arabic with an Argentine-looking tail', 'Asia/Riyadh', ['ar-EG']],
		['a subtag that only starts with "ar"', 'Asia/Dubai', ['en-arab', 'ar-AE']],
		['a tag Intl refuses, not Argentine', 'Europe/Madrid', ['es_ES', '']],
		['no information at all', undefined, []]
	])('sends %s to Ko-fi', (_name, timeZone, languages) => {
		expect(supportUrl({ timeZone, languages })).toBe(SUPPORT_DEFAULT);
	});

	it('points at the owner pages', () => {
		expect(SUPPORT_ARGENTINA).toBe('https://cafecito.app/aresiusxp');
		expect(SUPPORT_DEFAULT).toBe('https://ko-fi.com/aresiusxp');
	});
});

describe('detectSupportEnv', () => {
	afterEach(() => vi.restoreAllMocks());

	const timeZone = (zone: string | (() => never)) =>
		vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(
			() =>
				({
					resolvedOptions: () => {
						if (typeof zone === 'function') zone();
						return { timeZone: zone };
					}
				}) as Intl.DateTimeFormat
		);

	it('reads the device timezone and the language list', () => {
		timeZone('America/Argentina/Buenos_Aires');
		vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['es-AR', 'en']);
		expect(detectSupportEnv()).toEqual({
			timeZone: 'America/Argentina/Buenos_Aires',
			languages: ['es-AR', 'en']
		});
		expect(supportLink()).toBe(SUPPORT_ARGENTINA);
	});

	it('falls back to navigator.language when there is no list', () => {
		timeZone('Europe/Amsterdam');
		vi.spyOn(navigator, 'languages', 'get').mockReturnValue([]);
		vi.spyOn(navigator, 'language', 'get').mockReturnValue('es-AR');
		expect(detectSupportEnv().languages).toEqual(['es-AR']);
		expect(supportLink()).toBe(SUPPORT_ARGENTINA);
	});

	it('is Ko-fi for a Dutch device', () => {
		timeZone('Europe/Amsterdam');
		vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['nl-NL']);
		expect(supportLink()).toBe(SUPPORT_DEFAULT);
	});

	it('never throws: a browser that refuses to say gets Ko-fi', () => {
		timeZone(() => {
			throw new Error('no Intl');
		});
		vi.spyOn(navigator, 'languages', 'get').mockImplementation(() => {
			throw new Error('no navigator');
		});
		expect(detectSupportEnv()).toEqual({ timeZone: undefined, languages: [] });
		expect(supportLink()).toBe(SUPPORT_DEFAULT);
	});
});
