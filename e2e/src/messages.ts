import fs from 'node:fs';
import path from 'node:path';
import { WEB_DIR } from './env';

/**
 * The REAL message files of the web app (`web/messages/**`), so that the labels the suite
 * looks for can never drift from what the app renders. `{param}` templates are compiled here.
 */
export type Locale = 'es' | 'en';
export type Params = Record<string, string | number>;

export const LOCALE_TAGS: Record<Locale, string> = { es: 'es-AR', en: 'en-US' };

const catalogs = new Map<Locale, Record<string, string>>();

function messageFiles(dir: string, locale: Locale): string[] {
	const found: string[] = [];
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) found.push(...messageFiles(full, locale));
		else if (entry.name === `${locale}.json`) found.push(full);
	}
	return found.sort();
}

function catalog(locale: Locale): Record<string, string> {
	let loaded = catalogs.get(locale);
	if (loaded) return loaded;
	const root = path.join(WEB_DIR, 'messages');
	const files = messageFiles(root, locale);
	if (files.length === 0) throw new Error(`no ${locale}.json message files under ${root}`);
	loaded = {};
	for (const file of files) {
		const data = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<string, unknown>;
		for (const [key, value] of Object.entries(data)) {
			if (key === '$schema') continue;
			if (typeof value !== 'string') {
				throw new Error(
					`${file}: message "${key}" is not a plain string (variants are not supported here)`
				);
			}
			if (key in loaded) throw new Error(`${file}: duplicate message key "${key}"`);
			loaded[key] = value;
		}
	}
	catalogs.set(locale, loaded);
	return loaded;
}

function raw(locale: Locale, key: string): string {
	const text = catalog(locale)[key];
	if (text === undefined) throw new Error(`unknown message key "${key}" (${locale})`);
	return text;
}

/** The message with its `{param}`s filled in. Throws on an unknown key or a missing parameter. */
export function msg(locale: Locale, key: string, params: Params = {}): string {
	return raw(locale, key).replace(/\{(\w+)\}/g, (_, name: string) => {
		const value = params[name];
		if (value === undefined) throw new Error(`message "${key}" needs the parameter "${name}"`);
		return String(value);
	});
}

const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * A RegExp for the message: given parameters are matched literally, the others become
 * capture groups (in order of appearance). Anchored unless `partial`.
 */
export function msgPattern(
	locale: Locale,
	key: string,
	params: Params = {},
	{ partial = false }: { partial?: boolean } = {}
): RegExp {
	const source = raw(locale, key)
		.split(/(\{\w+\})/)
		.map((part) => {
			const token = /^\{(\w+)\}$/.exec(part);
			if (!token) return escapeRegExp(part);
			const value = params[token[1] as string];
			return value === undefined ? '(.+?)' : escapeRegExp(String(value));
		})
		.join('');
	return new RegExp(partial ? source : `^${source}$`);
}

/** Every Spanish-only character: none of them may ever appear on an English screen. */
export const SPANISH_CHARS = /[¡¿ñÑáéíóúüÁÉÍÓÚÜ]/u;

export function t(locale: Locale) {
	return (key: string, params?: Params) => msg(locale, key, params);
}
