import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { analyzeHtml } from '../../scripts/csp.mjs';
import { BRAND } from './brand';
import { locales } from './paraglide/runtime';

const root = fileURLToPath(new URL('../../', import.meta.url));
const read = (file: string) => readFileSync(join(root, file), 'utf8');
const css = read('static/privacy.css');

const CONTACT = 'aresiusxp@gmail.com';
/** The version of the terms the server asks players to accept (server/internal/**, TermsVersion). */
const TERMS_VERSION = '2026-10';

/** The static legal pages: plain HTML, siblings that share privacy.css and the #es/#en/#nl switch. */
const PAGES = [
	{ name: 'privacy', file: 'static/privacy.html', path: '/privacy' },
	{ name: 'terms', file: 'static/terms.html', path: '/terms' },
	{ name: 'delete-account', file: 'static/delete-account.html', path: '/delete-account' }
] as const;
const PAGE_PATHS = new Set<string>(['/', ...PAGES.map((page) => page.path)]);

/** Every host a legal page may link to (links only: nothing is embedded from other sites). */
const EXTERNAL_HOSTS = new Set([
	'tuttifrutti.aresius.xyz',
	'github.com',
	'developers.google.com',
	'policies.google.com',
	'myaccount.google.com',
	'www.cloudflare.com'
]);

const MONTHS: Record<string, string[]> = {
	en: [
		'january',
		'february',
		'march',
		'april',
		'may',
		'june',
		'july',
		'august',
		'september',
		'october',
		'november',
		'december'
	],
	es: [
		'enero',
		'febrero',
		'marzo',
		'abril',
		'mayo',
		'junio',
		'julio',
		'agosto',
		'septiembre',
		'octubre',
		'noviembre',
		'diciembre'
	],
	nl: [
		'januari',
		'februari',
		'maart',
		'april',
		'mei',
		'juni',
		'juli',
		'augustus',
		'september',
		'oktober',
		'november',
		'december'
	]
};
const UPDATED: Record<string, RegExp> = {
	en: /Last updated:\s*(\d{1,2}) ([a-z]+) (\d{4})/i,
	es: /Última actualización:\s*(\d{1,2}) de ([a-zñ]+) de (\d{4})/i,
	nl: /Laatst bijgewerkt:\s*(\d{1,2}) ([a-z]+) (\d{4})/i
};

function sectionOf(html: string, locale: string): string {
	const start = html.indexOf(`<section class="doc" id="${locale}" lang="${locale}">`);
	const end = html.indexOf('</section>', start);
	return start < 0 || end < 0 ? '' : html.slice(start, end);
}

/** The visible text of some markup: tags removed, whitespace collapsed. */
const textOf = (markup: string) => markup.replace(/<[^>]*>/g, '').replace(/\s+/g, ' ');

const hrefs = (markup: string) =>
	[...markup.matchAll(/<a\b[^>]*?\shref="([^"]*)"/g)].map((m) => m[1]);

/** The "last updated" date of a language block as YYYY-MM-DD, or null. */
function updatedOn(locale: string, section: string): string | null {
	const found = UPDATED[locale].exec(textOf(section));
	if (!found) return null;
	const month = MONTHS[locale].indexOf(found[2].toLowerCase());
	if (month < 0) return null;
	return `${found[3]}-${String(month + 1).padStart(2, '0')}-${found[1].padStart(2, '0')}`;
}

function serverTermsVersion(): string | null {
	const dir = join(root, '..', 'server', 'internal');
	if (!existsSync(dir)) return null;
	for (const entry of readdirSync(dir, { recursive: true, withFileTypes: true })) {
		if (!entry.isFile() || !entry.name.endsWith('.go') || entry.name.endsWith('_test.go')) continue;
		const found = /\bTermsVersion\b[^=\n]*=\s*"([^"]+)"/.exec(
			readFileSync(join(entry.parentPath, entry.name), 'utf8')
		);
		if (found) return found[1];
	}
	return null;
}

describe.each(PAGES)('$file (a legal page)', ({ file, path }) => {
	const html = read(file);
	const sections = Object.fromEntries(locales.map((l) => [l, sectionOf(html, l)]));
	const counts = (re: RegExp) => locales.map((l) => [...sections[l].matchAll(re)].length);

	it('has one section per app language, each linked from the language bar and styled by :target', () => {
		expect(html.split('<section class="doc"').slice(1)).toHaveLength(locales.length);
		for (const locale of locales) {
			expect(sections[locale], locale).not.toBe('');
			expect(html).toContain(`<a href="#${locale}" hreflang="${locale}" lang="${locale}">`);
			expect(css).toContain(`#${locale}`);
		}
		expect(html).toContain('<html lang="en">');
		expect(html).toContain('<link rel="stylesheet" href="/privacy.css" />');
		expect(html).toContain('<nav class="bar"');
		expect(html).toContain('<a class="back" href="/" rel="external">');
		expect(html).toMatch(/<meta\s+name="viewport"/);
	});

	it('has the same section structure in every language', () => {
		const numbers = locales.map((l) =>
			[...sections[l].matchAll(/<h2>\s*(\d+)\./g)].map((m) => m[1])
		);
		expect(numbers[0].length).toBeGreaterThanOrEqual(5);
		expect(numbers[0]).toEqual(numbers[0].map((_, i) => String(i + 1)));
		for (const n of numbers) expect(n).toEqual(numbers[0]);
		for (const re of [/<h1>/g, /<h2>/g, /<h3>/g, /<li>/g, /<ol>/g, /<ul>/g, /<dt>/g]) {
			const found = counts(re);
			for (const n of found) expect(n, String(re)).toBe(found[0]);
		}
	});

	it('links only to places that exist, in the reader language', () => {
		const ids = new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1]));
		for (const href of hrefs(html)) {
			if (href.startsWith('#')) {
				expect(ids.has(href.slice(1)), href).toBe(true);
			} else if (href.startsWith('mailto:')) {
				expect(href).toBe(`mailto:${CONTACT}`);
			} else if (href.startsWith('/')) {
				const [target, fragment] = href.split('#');
				expect(PAGE_PATHS.has(target), href).toBe(true);
				if (fragment !== undefined) expect(locales as readonly string[], href).toContain(fragment);
			} else {
				const url = new URL(href);
				expect(url.protocol, href).toBe('https:');
				expect(url.username + url.password, href).toBe('');
				expect(EXTERNAL_HOSTS.has(url.host), href).toBe(true);
			}
		}
		for (const locale of locales) {
			for (const href of hrefs(sections[locale])) {
				if (href.startsWith('/') && href !== '/')
					expect(href.split('#')[1], `${locale}: ${href}`).toBe(locale);
			}
		}
	});

	it('links to the same places in every language', () => {
		const targets = locales.map((l) =>
			hrefs(sections[l])
				.map((href) => href.replace(/#(?:es|en|nl)$/, '#lang'))
				.sort()
		);
		for (const t of targets) expect(t).toEqual(targets[0]);
	});

	it('is plain HTML the policy allows: no script, <style>, handler or style attribute', () => {
		const { problems, styleAttrs } = analyzeHtml(html);
		// analyzeHtml expects the SPA shell, so only its generic findings are used
		expect(problems.filter((p) => !p.startsWith('expected exactly one SvelteKit'))).toEqual([]);
		expect(styleAttrs).toEqual([]);
		expect(html).not.toMatch(/<script\b|<style\b|\sstyle\s*=|\son[a-z]+\s*=/i);
		expect(html).not.toMatch(/javascript:/i);
		expect(html).not.toMatch(
			/<(?:img|link|script|iframe|source|object|embed)\b[^>]*\s(?:src|href)\s*=\s*["']?(?:https?:)?\/\//i
		);
	});

	it('writes the brand exactly like the constant, names the app in the title and in every language', () => {
		for (const [found] of html.matchAll(/tutti\s+frutti/gi)) expect(found).toBe(BRAND);
		expect(html).toMatch(new RegExp(`<title>[^<]*${BRAND}</title>`));
		for (const locale of locales) expect(sections[locale], locale).toContain(BRAND);
	});

	it('never names the game after the call: Basta is only the Spanish call to end a round', () => {
		expect(html).not.toMatch(/tutti\s+frutti[^<]{0,3}basta|basta[^<]{0,3}tutti\s+frutti/i);
		for (const locale of ['en', 'nl']) expect(sections[locale], locale).not.toMatch(/basta/i);
		for (const [found] of sections.es.matchAll(/basta/gi)) expect(found).toBe('BASTA');
	});

	it('shows the same last-updated date in every language', () => {
		const dates = locales.map((l) => updatedOn(l, sections[l]));
		for (const date of dates) {
			expect(date).toMatch(/^\d{4}-\d{2}-\d{2}$/);
			expect(Number.isNaN(Date.parse(date ?? '')), String(date)).toBe(false);
		}
		expect(new Set(dates).size, dates.join(' ')).toBe(1);
	});

	it('names the contact and is served at its own path', () => {
		for (const locale of locales) expect(sections[locale], locale).toContain(`mailto:${CONTACT}`);
		expect(PAGE_PATHS.has(path)).toBe(true);
	});

	it.skipIf(path === '/privacy')('claims no legal compliance or certification', () => {
		// the privacy policy quotes Google's own "Limited Use" wording, so it is left out
		expect(textOf(html)).not.toMatch(
			/compliant|certified|certificad[oa]|gecertificeerd|cumple con (?:la|el) |voldoet aan de (?:AVG|wet)/i
		);
	});
});

describe('static/terms.html', () => {
	const html = read('static/terms.html');
	const sections = Object.fromEntries(locales.map((l) => [l, textOf(sectionOf(html, l))]));
	const raw = Object.fromEntries(locales.map((l) => [l, sectionOf(html, l)]));

	it('shows the version of the terms in every language, the one the server asks to accept', () => {
		for (const locale of locales) {
			expect(raw[locale], locale).toContain(`<code>${TERMS_VERSION}</code>`);
			expect([...raw[locale].matchAll(/<code>(\d{4}-\d{2})<\/code>/g)].map((m) => m[1])).toEqual(
				Array(2).fill(TERMS_VERSION)
			);
		}
		expect(html).toContain('<title>Terms of use and community rules – Tutti Frutti</title>');
	});

	it.skipIf(serverTermsVersion() === null)('shows the TermsVersion constant of the server', () => {
		expect(serverTermsVersion()).toBe(TERMS_VERSION);
	});

	it('states the same rules in every language', () => {
		const facts: Record<string, string[]> = {
			en: [
				'intended for people aged 13 and over',
				'does not replace any rule about age or permission',
				'harassment or bullying',
				'hate or discrimination',
				'sexual content',
				'graphic violence or threats',
				'encouraging self-harm',
				'personal data of other people',
				'impersonating another person',
				'spam or advertising',
				'illegal content',
				'can be wrong or unfair',
				'comment of up to 280 characters',
				'regularly and prioritise serious safety concerns',
				'do not promise to review every report in real time',
				'never causes an automatic penalty',
				'removes you from the current game and prevents future games with that player',
				'clears their cookies, or uses another browser or device',
				'remove content',
				'close a game',
				'restrict a browser identity or an account',
				'remove a saved profile',
				'provided as it is',
				'gives no benefit in the game',
				'asks you to accept the new version again'
			],
			es: [
				'pensado para personas de 13 años o más',
				'no reemplaza ninguna regla sobre edad o permisos',
				'acosar o hostigar',
				'el odio o la discriminación',
				'contenido sexual',
				'violencia gráfica o amenazas',
				'alentar la autolesión',
				'datos personales de otras personas',
				'hacerte pasar por otra persona',
				'spam o publicidad',
				'contenido ilegal',
				'puede equivocarse o ser injusto',
				'comentario de hasta 280 caracteres',
				'con regularidad y damos prioridad a los problemas graves de seguridad',
				'No prometemos revisar cada reporte en tiempo real',
				'nunca provoca una sanción automática',
				'te saca a vos de la partida actual y evita futuras partidas con la identidad de navegador reconocida',
				'borra sus cookies, o usa otro navegador u otro dispositivo',
				'quitar contenido',
				'cerrar una partida',
				'restringir a una identidad de navegador o a una cuenta',
				'borrar un perfil guardado',
				'Se ofrece tal cual',
				'no da ningún beneficio en el juego',
				'el juego te pide aceptar de nuevo la nueva versión'
			],
			nl: [
				'bedoeld voor mensen van 13 jaar en ouder',
				'vervangt geen regel over leeftijd of toestemming',
				'intimidatie of pesten',
				'haat of discriminatie',
				'seksuele inhoud',
				'expliciet geweld of bedreigingen',
				'aanmoediging van zelfbeschadiging',
				'persoonsgegevens van anderen',
				'je voordoen als iemand anders',
				'spam of reclame',
				'illegale inhoud',
				'fout of onrechtvaardig zijn',
				'opmerking van maximaal 280 tekens',
				'meldingen regelmatig en geven voorrang aan ernstige veiligheidsproblemen',
				'beloven niet elke melding in realtime te bekijken',
				'leidt nooit tot een automatische straf',
				'haalt jou uit het huidige spel en voorkomt toekomstige spellen met de herkende browseridentiteit',
				'wist iemand zijn cookies of gebruikt die een andere browser of een ander apparaat',
				'inhoud verwijderen',
				'een spel sluiten',
				'een browseridentiteit of een account beperken',
				'een opgeslagen profiel verwijderen',
				'aangeboden zoals het is',
				'geeft geen voordeel in het spel',
				'vraagt het spel je de nieuwe versie opnieuw te accepteren'
			]
		};
		for (const locale of locales) {
			for (const fact of facts[locale])
				expect(sections[locale], `${locale}: ${fact}`).toContain(fact);
		}
	});

	it('names the block action like the app does and the way to report', () => {
		expect(raw.en).toContain('<em>Block and leave</em>');
		expect(raw.en).toContain('<em>Report</em>');
	});

	it('points to the privacy policy and to the account deletion page, and never promises a response time', () => {
		for (const locale of locales) {
			expect(raw[locale], locale).toContain(`href="/privacy#${locale}"`);
			expect(raw[locale], locale).toContain(`href="/delete-account#${locale}"`);
			expect(raw[locale], locale).toContain(`mailto:${CONTACT}`);
			// the owner decides response times: the page names none (the only figure with a unit is the 280 limit)
			expect(sections[locale], locale).not.toMatch(
				/\b\d+\s*(?:hours?|horas?|uur|uren|days?|días?|dagen)\b/i
			);
		}
	});
});

describe('static/delete-account.html', () => {
	const html = read('static/delete-account.html');
	const raw = Object.fromEntries(locales.map((l) => [l, sectionOf(html, l)]));
	const text = Object.fromEntries(locales.map((l) => [l, textOf(raw[l])]));
	const messages = (locale: string) =>
		JSON.parse(read(`messages/account/${locale}.json`)) as Record<string, string>;

	it('shows the app name, the package and the developer contact at the top of every language', () => {
		for (const locale of locales) {
			const top = raw[locale].slice(0, raw[locale].indexOf('<h2>1.'));
			expect(top, locale).toMatch(/<h1>[^<]*Tutti Frutti[^<]*<\/h1>/);
			expect(top, locale).toContain('<div class="summary">');
			expect(top, locale).toContain('xyz.aresius.tuttifrutti');
			expect(top, locale).toContain('href="https://tuttifrutti.aresius.xyz"');
			expect(top, locale).toContain(`href="mailto:${CONTACT}"`);
		}
	});

	it('gives the steps with the labels the app really shows', () => {
		for (const locale of locales) {
			const strings = messages(locale);
			const steps = /<ol>([\s\S]*?)<\/ol>/.exec(raw[locale])?.[1] ?? '';
			expect([...steps.matchAll(/<li>/g)], locale).toHaveLength(4);
			for (const key of ['account_stats_link', 'stats_delete', 'stats_delete_confirm'])
				expect(steps, `${locale}: ${key}`).toContain(`<em>${strings[key]}</em>`);
		}
	});

	it('works without the app, and without signing in: it explains the email route', () => {
		const facts: Record<string, string[]> = {
			en: [
				'you do not need to install the Android app',
				'Write from any email address to',
				'stores no email address from Google',
				'proves they control',
				'We will reply with instructions to verify it',
				'We answer within 30 days'
			],
			es: [
				'no hace falta instalar la app de Android',
				'Escribí desde cualquier dirección de correo a',
				'no guarda ningún correo de Google',
				'demuestre que controla',
				'Te vamos a responder con instrucciones para verificarlo',
				'Respondemos dentro de los 30 días'
			],
			nl: [
				'hoeft de Android-app niet te installeren',
				'Stuur vanaf een willekeurig e-mailadres een bericht naar',
				'geen e-mailadres van Google bewaart',
				'aantoont dat hij of zij het beheert',
				'We antwoorden met instructies om dat te controleren',
				'We antwoorden binnen 30 dagen'
			]
		};
		for (const locale of locales) {
			for (const fact of facts[locale]) expect(text[locale], `${locale}: ${fact}`).toContain(fact);
		}
	});

	it('says what is deleted now and what is kept, with the retention figures of the policy', () => {
		for (const locale of locales) {
			for (const cookie of ['tf_session', 'tf_guest'])
				expect(raw[locale]).toContain(`<code>${cookie}</code>`);
			for (const figure of ['30', '365', '90', '7', '6'])
				expect(text[locale], `${locale}: ${figure}`).toMatch(new RegExp(`\\b${figure}\\b`));
			expect(raw[locale]).toContain('https://myaccount.google.com/permissions');
		}
		const facts: Record<string, string[]> = {
			en: [
				'Content already in a running game room',
				'Safety records',
				'a new, empty account is created',
				'cannot be undone',
				'does not clear them, and neither does uninstalling the Android app',
				'Your Google account</strong> is not touched'
			],
			es: [
				'Lo que ya está en una sala en curso',
				'Los registros de seguridad',
				'se crea una cuenta nueva y vacía',
				'no se puede deshacer',
				'no los borra, y desinstalar la app de Android tampoco',
				'Tu cuenta de Google</strong> no se toca'
			],
			nl: [
				'Wat al in een lopende kamer staat',
				'Veiligheidsgegevens',
				'ontstaat er een nieuw, leeg account',
				'niet ongedaan worden gemaakt',
				'wist ze niet, en de Android-app verwijderen ook niet',
				'Je Google-account</strong> blijft onaangeroerd'
			]
		};
		for (const locale of locales) {
			const haystack = `${text[locale]} ${raw[locale].replace(/\s+/g, ' ')}`;
			for (const fact of facts[locale]) expect(haystack, `${locale}: ${fact}`).toContain(fact);
		}
	});

	it('names the privacy policy page by its path and links to it and to the terms', () => {
		for (const locale of locales) {
			expect(text[locale], locale).toContain('tuttifrutti.aresius.xyz/privacy');
			expect(raw[locale], locale).toContain(`href="/privacy#${locale}"`);
			expect(raw[locale], locale).toContain(`href="/terms#${locale}"`);
		}
	});
});
