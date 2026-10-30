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
	'www.cloudflare.com',
	// the data protection authorities a player can complain to (privacy policy)
	'www.autoriteitpersoonsgegevens.nl',
	'www.argentina.gob.ar'
]);

/**
 * The app's buttons and screens each page quotes (in <em>, and <em> is used for nothing else),
 * as message keys: the pages must name them exactly like the app does in every language.
 */
const QUOTED_LABELS: Record<(typeof PAGES)[number]['name'], string[]> = {
	privacy: ['review_judge_ask', 'account_stats_link', 'stats_delete'],
	terms: [
		'report_players_aria',
		'report_send',
		'report_menu_block',
		'legal_sheet_aria',
		'blocked_link',
		'blocked_unblock'
	],
	'delete-account': ['account_stats_link', 'stats_delete', 'stats_delete_confirm']
};

/**
 * Deadlines for data requests and for deleting an account by email, the same on all three pages:
 * without undue delay, at the latest one month; two more months for a complex request only where
 * the law allows it, never in Argentina, whose Ley 25.326 is stricter: access within 10 days
 * (art. 14), correction or deletion within 5 business days (art. 16).
 */
const DEADLINES: Record<
	string,
	{ agreed: string[]; access: string; extension: string; notInArgentina: string }
> = {
	en: {
		// lower case: some pages start a sentence with them
		agreed: [
			'without undue delay',
			'at the latest within one month',
			'where the law allows it (not in argentina), a complex request can take two more months',
			'we tell you within the first month',
			'within 5 business days'
		],
		access: 'within 10 days for access to your data',
		extension: 'two more months',
		notInArgentina: '(not in Argentina)'
	},
	es: {
		agreed: [
			'sin demoras indebidas',
			'como máximo dentro de un mes',
			'donde la ley lo permite (no en argentina), un pedido complejo puede llevar dos meses más',
			'en ese caso te avisamos dentro del primer mes',
			'dentro de los 5 días hábiles'
		],
		access: 'dentro de los 10 días para acceder a tus datos',
		extension: 'dos meses más',
		notInArgentina: '(no en Argentina)'
	},
	nl: {
		agreed: [
			'zonder onnodige vertraging',
			'uiterlijk binnen een maand',
			'waar de wet het toelaat (niet in argentinië), kan een ingewikkeld verzoek twee maanden langer duren',
			'dan laten we je dat binnen de eerste maand weten',
			'binnen 5 werkdagen'
		],
		access: 'binnen 10 dagen voor inzage in je gegevens',
		extension: 'twee maanden langer',
		notInArgentina: '(niet in Argentinië)'
	}
};

/** Wording these pages used before and that is no longer true (or never was a promise we keep). */
const STALE: Record<string, RegExp> = {
	en: /we aim to keep|about 7 days|within 30 days|because you chose to use it|keeps your IP address/i,
	es: /buscamos conservarlos|unos 7 días|dentro de los 30 días|porque elegiste usarlo|guarda tu dirección IP en su memoria/i,
	nl: /streven ernaar|ongeveer 7 dagen|binnen 30 dagen|omdat je ervoor hebt gekozen|houdt hij je IP-adres/i
};

/** Every message of the app in one language (messages/<locale>.json and every area folder), by key. */
function appMessages(locale: string): Record<string, string> {
	const files = [
		`messages/${locale}.json`,
		...readdirSync(join(root, 'messages'), { withFileTypes: true })
			.filter((entry) => entry.isDirectory())
			.map((entry) => `messages/${entry.name}/${locale}.json`)
			.filter((file) => existsSync(join(root, file)))
	];
	return Object.assign(
		{},
		...files.map((file) => JSON.parse(read(file)) as Record<string, string>)
	);
}

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

describe.each(PAGES)('$file (a legal page)', ({ name, file, path }) => {
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

	it('quotes the labels of the app exactly as the app shows them, in every language', () => {
		for (const locale of locales) {
			const strings = appMessages(locale);
			const expected = QUOTED_LABELS[name].map((key) => {
				expect(strings[key], `${locale}: ${key}`).toBeTypeOf('string');
				return strings[key];
			});
			const quoted = [...sections[locale].matchAll(/<em>([^<]*)<\/em>/g)].map((m) =>
				m[1].replace(/\s+/g, ' ').trim()
			);
			expect([...new Set(quoted)].sort(), locale).toEqual([...new Set(expected)].sort());
		}
	});

	it('no longer uses wording that is not true any more', () => {
		for (const locale of locales)
			expect(textOf(sections[locale]), locale).not.toMatch(STALE[locale]);
	});

	it('states the same deadlines for data requests as the other legal pages, the stricter ones of Ley 25.326 for Argentina', () => {
		for (const locale of locales) {
			const text = textOf(sections[locale]);
			const { agreed, access, extension, notInArgentina } = DEADLINES[locale];
			expect(text, locale).toContain('Ley 25.326');
			for (const phrase of agreed)
				expect(text.toLowerCase(), `${locale}: ${phrase}`).toContain(phrase);
			// the deletion page only handles deletion; the other two also cover access requests
			if (name !== 'delete-account') expect(text, `${locale}: ${access}`).toContain(access);
			// every page offers the two-month extension (the terms for data requests and for deletion by
			// email), and always leaves Argentina out in the same sentence: no absolute promise elsewhere
			const offered = text.split(/(?<=[.;])\s/).filter((s) => s.includes(extension));
			expect(offered.length, locale).toBeGreaterThanOrEqual(name === 'terms' ? 2 : 1);
			for (const sentence of offered) expect(sentence, locale).toContain(notInArgentina);
		}
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
				'The other person is not told',
				'we cannot tell you the outcome',
				'why you think the content is illegal',
				'the room code, the approximate time, the nickname and the text',
				'your name and email address (not needed if your notice is about sexual abuse of children)',
				'you make the notice in good faith',
				'removes you from the current game and prevents future games with that player',
				'clears their cookies, or uses another browser or device',
				'A person, the one who runs the game, reads every report and decides',
				'No decision about content or about a player is made automatically',
				'the number of reports never causes a penalty by itself',
				'The AI judge only gives scoring advice during the review, and the players decide',
				'dismiss it',
				'close a game room',
				'remove a saved profile',
				'restrict a browser identity or an account, for a limited time or permanently',
				'we undo the decision at once',
				"a threat to someone's life, self-harm, grooming, or sexual content involving minors",
				'Our single point of contact, for players and for authorities',
				'You can write to us in Dutch, English or Spanish',
				'provided as it is',
				'On the website only (not in the Android app)',
				'gives no benefit in the game, gets you nothing in return',
				'(Cafecito, Ko-fi) have their own terms and only accept supporters aged 18 or older',
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
				'La otra persona no recibe ningún aviso',
				'no podemos contarte el resultado',
				'por qué creés que el contenido es ilegal',
				'el código de la sala, la hora aproximada, el apodo y el texto',
				'tu nombre y tu correo electrónico (no hacen falta si el aviso trata sobre abuso sexual de menores)',
				'hacés el aviso de buena fe',
				'te saca a vos de la partida actual y evita futuras partidas con la identidad de navegador reconocida',
				'borra sus cookies, o usa otro navegador u otro dispositivo',
				'Una persona, quien está a cargo del juego, lee cada reporte y decide',
				'Ninguna decisión sobre un contenido o sobre un jugador se toma de forma automática',
				'la cantidad de reportes nunca provoca una sanción por sí sola',
				'El juez de IA solo da una sugerencia sobre los puntos durante la revisión, y deciden los jugadores',
				'descartarlo',
				'cerrar una sala',
				'borrar un perfil guardado',
				'restringir a una identidad de navegador o a una cuenta, por un tiempo o para siempre',
				'deshacemos la decisión de inmediato',
				'una amenaza contra la vida de alguien, autolesiones, grooming o contenido sexual con menores',
				'Nuestro único punto de contacto, para jugadores y para autoridades',
				'Podés escribirnos en neerlandés, inglés o español',
				'Se ofrece tal cual',
				'Solo en el sitio web (no en la app de Android)',
				'no da ningún beneficio en el juego, no te da nada a cambio',
				'(Cafecito, Ko-fi) tienen sus propios términos y solo aceptan a personas de 18 años o más',
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
				'De ander krijgt hier niets van te horen',
				'we kunnen je de uitkomst niet laten weten',
				'waarom je denkt dat de inhoud illegaal is',
				'de kamercode, het tijdstip ongeveer, de bijnaam en de tekst',
				'je naam en e-mailadres (niet nodig als je melding over seksueel misbruik van kinderen gaat)',
				'je de melding te goeder trouw doet',
				'haalt jou uit het huidige spel en voorkomt toekomstige spellen met de herkende browseridentiteit',
				'wist iemand zijn cookies of gebruikt die een andere browser of een ander apparaat',
				'Een mens, degene die het spel beheert, leest elke melding en beslist',
				'Geen enkele beslissing over inhoud of over een speler wordt automatisch genomen',
				'het aantal meldingen leidt nooit vanzelf tot een straf',
				'De AI-scheidsrechter geeft alleen advies over de punten tijdens de beoordeling, en de spelers beslissen',
				'de melding afwijzen',
				'een kamer sluiten',
				'een opgeslagen profiel verwijderen',
				'een browseridentiteit of een account beperken, tijdelijk of voorgoed',
				'draaien we de beslissing meteen terug',
				'een bedreiging van iemands leven, zelfbeschadiging, grooming of seksuele inhoud met minderjarigen',
				'Ons centrale contactpunt, voor spelers en voor autoriteiten',
				'Je kunt ons schrijven in het Nederlands, Engels of Spaans',
				'aangeboden zoals het is',
				'Alleen op de website (niet in de Android-app)',
				'geeft geen voordeel in het spel, je krijgt er niets voor terug',
				'(Cafecito, Ko-fi) hebben hun eigen voorwaarden en accepteren alleen mensen van 18 jaar of ouder',
				'vraagt het spel je de nieuwe versie opnieuw te accepteren'
			]
		};
		for (const locale of locales) {
			for (const fact of facts[locale])
				expect(sections[locale], `${locale}: ${fact}`).toContain(fact);
		}
	});

	it('states the response times the owner commits to, and no other deadline', () => {
		const facts: Record<string, string[]> = {
			en: [
				'We confirm that we received an emailed notice within 3 days, decide on it within 7 days, and tell you what we decided and how you can appeal',
				'We look at the case again and answer within 14 days',
				'we aim to check them every day, and we decide on each one within 7 days at the latest',
				'we act within 24 hours of seeing them',
				'we confirm we received them within 3 days and decide within 7 days',
				'Appeals: we answer within 14 days',
				'Requests about your data: without undue delay, at the latest within one month; where the law allows it (not in Argentina), a complex request can take two more months, and if so we tell you within the first month; if you live in Argentina, within 10 days for access to your data and within 5 business days for correction or deletion (Ley 25.326, arts. 14 and 16',
				'Deleting an account by email: we answer without undue delay, at the latest within one month; where the law allows it (not in Argentina), a complex request can take two more months, and if so we tell you within the first month. Once you have shown that the account is yours, we delete it within one month at the latest. If you live in Argentina, we answer within 5 business days and delete it within 5 business days after you have shown that it is yours (Ley 25.326, art. 16)',
				'Any other message: we confirm we received it within 7 days',
				'call 112 (EU and the Netherlands) or 911 (Argentina)',
				"When we learn of a threat to someone's life or safety, we report it to the police"
			],
			es: [
				'Te confirmamos que recibimos un aviso por correo dentro de los 3 días, decidimos dentro de los 7 días y te contamos qué decidimos y cómo podés apelar',
				'Volvemos a mirar el caso y te respondemos dentro de los 14 días',
				'nuestro objetivo es revisarlos todos los días, y decidimos cada uno dentro de los 7 días como máximo',
				'actuamos dentro de las 24 horas desde que los vemos',
				'confirmamos que los recibimos dentro de los 3 días y decidimos dentro de los 7 días',
				'Apelaciones: respondemos dentro de los 14 días',
				'Pedidos sobre tus datos: sin demoras indebidas y como máximo dentro de un mes; donde la ley lo permite (no en Argentina), un pedido complejo puede llevar dos meses más, y en ese caso te avisamos dentro del primer mes; si vivís en Argentina, dentro de los 10 días para acceder a tus datos y dentro de los 5 días hábiles para corregirlos o eliminarlos (Ley 25.326, arts. 14 y 16',
				'Eliminar una cuenta por correo: te respondemos sin demoras indebidas y como máximo dentro de un mes; donde la ley lo permite (no en Argentina), un pedido complejo puede llevar dos meses más, y en ese caso te avisamos dentro del primer mes. Una vez que demostraste que la cuenta es tuya, la eliminamos como máximo dentro de un mes. Si vivís en Argentina, te respondemos dentro de los 5 días hábiles y eliminamos la cuenta dentro de los 5 días hábiles desde que demostrás que es tuya (Ley 25.326, art. 16)',
				'Cualquier otro mensaje: te confirmamos que lo recibimos dentro de los 7 días',
				'llamá al 112 (UE y Países Bajos) o al 911 (Argentina)',
				'Cuando nos enteramos de una amenaza contra la vida o la seguridad de alguien, la denunciamos a la policía'
			],
			nl: [
				'We bevestigen binnen 3 dagen dat we een melding per e-mail hebben ontvangen, beslissen binnen 7 dagen en laten je weten wat we hebben besloten en hoe je bezwaar kunt maken',
				'We bekijken de zaak opnieuw en antwoorden binnen 14 dagen',
				'we proberen ze elke dag te bekijken en beslissen over elke melding uiterlijk binnen 7 dagen',
				'we handelen binnen 24 uur nadat we ze hebben gezien',
				'we bevestigen de ontvangst binnen 3 dagen en beslissen binnen 7 dagen',
				'Bezwaren: we antwoorden binnen 14 dagen',
				'Verzoeken over je gegevens: zonder onnodige vertraging en uiterlijk binnen een maand; waar de wet het toelaat (niet in Argentinië), kan een ingewikkeld verzoek twee maanden langer duren, en dan laten we je dat binnen de eerste maand weten; woon je in Argentinië, dan binnen 10 dagen voor inzage in je gegevens en binnen 5 werkdagen voor correctie of verwijdering (Ley 25.326, art. 14 en 16',
				'Een account per e-mail verwijderen: we antwoorden zonder onnodige vertraging en uiterlijk binnen een maand; waar de wet het toelaat (niet in Argentinië), kan een ingewikkeld verzoek twee maanden langer duren, en dan laten we je dat binnen de eerste maand weten. Zodra je hebt aangetoond dat het account van jou is, verwijderen we het uiterlijk binnen een maand. Woon je in Argentinië, dan antwoorden we binnen 5 werkdagen en verwijderen we het account binnen 5 werkdagen nadat je hebt aangetoond dat het van jou is (Ley 25.326, art. 16)',
				'Elk ander bericht: we bevestigen binnen 7 dagen dat we het hebben ontvangen',
				'bel dan 112 (EU en Nederland) of 911 (Argentinië)',
				'Horen we van een bedreiging van iemands leven of veiligheid, dan melden we dat bij de politie'
			]
		};
		for (const locale of locales) {
			for (const fact of facts[locale])
				expect(sections[locale], `${locale}: ${fact}`).toContain(fact);
			// the only deadlines are the committed ones: 3, 7 and 14 days, 24 hours (and one month), plus
			// Argentina's 10 days and 5 business days (Ley 25.326)
			const deadlines = [
				...sections[locale].matchAll(
					/\b(\d+)\s*(business days?|días hábiles|werkdagen|hours?|horas?|uur|uren|days?|días?|dagen)\b/gi
				)
			].map(
				(m) =>
					`${m[1]} ${/business|hábiles|werk/i.test(m[2]) ? 'business days' : /^(?:hours?|horas?|uur|uren)$/i.test(m[2]) ? 'hours' : 'days'}`
			);
			expect([...new Set(deadlines)].sort(), locale).toEqual(
				['3 days', '7 days', '10 days', '14 days', '24 hours', '5 business days'].sort()
			);
		}
	});

	it('states the governing law word for word, keeping the consumer protection of the country of residence', () => {
		const law: Record<string, string> = {
			en: "These terms are governed by Dutch law. If you use Tutti Frutti as a consumer, this choice does not take away the protection of the mandatory rules of the country where you habitually live, which still apply to you. You may bring a dispute before the competent court in the Netherlands or the courts of the place where you live, as far as that place's law allows; we would bring any claim against you only before the courts of the country where you live. Nothing here limits a right the law gives you that cannot be waived.",
			es: 'Estos términos se rigen por la ley de los Países Bajos. Si usás Tutti Frutti como consumidor, esta elección no te quita la protección de las normas imperativas del país donde vivís habitualmente, que se siguen aplicando. Podés presentar un reclamo ante el tribunal competente de los Países Bajos o ante los jueces del lugar donde vivís, en la medida en que la ley de ese lugar lo permita; cualquier reclamo nuestro contra vos lo haríamos solo ante los jueces del país donde vivís. Nada de esto limita un derecho que la ley te reconoce y al que no se puede renunciar.',
			nl: 'Op deze voorwaarden is Nederlands recht van toepassing. Gebruik je Tutti Frutti als consument, dan verlies je hierdoor niet de bescherming van de dwingende regels van het land waar je gewoonlijk woont; die blijven gelden. Je kunt een geschil voorleggen aan de bevoegde rechter in Nederland of aan de rechter van je woonplaats, voor zover het recht daar dat toestaat; een vordering tegen jou stellen we alleen in bij de rechter van het land waar je woont. Niets hierin beperkt een recht dat de wet je geeft en waarvan niet kan worden afgeweken.'
		};
		for (const locale of locales) expect(sections[locale], locale).toContain(law[locale]);
	});

	it('points to the privacy policy, to the account deletion page and to the contact for notices', () => {
		for (const locale of locales) {
			expect(raw[locale], locale).toContain(`href="/privacy#${locale}"`);
			expect(raw[locale], locale).toContain(`href="/delete-account#${locale}"`);
			// reports by email, appeals and the single point of contact all name the address
			expect(raw[locale].split(`mailto:${CONTACT}`).length - 1, locale).toBeGreaterThanOrEqual(3);
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
				'We answer without undue delay and at the latest within one month',
				'where the law allows it (not in Argentina), a complex request can take two more months, and if so we tell you within the first month',
				'If you live in Argentina, we answer within 5 business days and delete the account within 5 business days after you have shown that it is yours (Ley 25.326, art. 16)',
				'Once you have shown that the account is yours, we delete it within one month at the latest',
				"A parent or guardian can ask the same for their child's account"
			],
			es: [
				'no hace falta instalar la app de Android',
				'Escribí desde cualquier dirección de correo a',
				'no guarda ningún correo de Google',
				'demuestre que controla',
				'Te vamos a responder con instrucciones para verificarlo',
				'Respondemos sin demoras indebidas y como máximo dentro de un mes',
				'donde la ley lo permite (no en Argentina), un pedido complejo puede llevar dos meses más, y en ese caso te avisamos dentro del primer mes',
				'Si vivís en Argentina, te respondemos dentro de los 5 días hábiles y eliminamos la cuenta dentro de los 5 días hábiles desde que demostrás que es tuya (Ley 25.326, art. 16)',
				'Una vez que demostraste que la cuenta es tuya, la eliminamos como máximo dentro de un mes',
				'Una madre, un padre o un tutor puede pedir lo mismo para la cuenta de su hijo o hija'
			],
			nl: [
				'hoeft de Android-app niet te installeren',
				'Stuur vanaf een willekeurig e-mailadres een bericht naar',
				'geen e-mailadres van Google bewaart',
				'aantoont dat hij of zij het beheert',
				'We antwoorden met instructies om dat te controleren',
				'We antwoorden zonder onnodige vertraging en uiterlijk binnen een maand',
				'waar de wet het toelaat (niet in Argentinië), kan een ingewikkeld verzoek twee maanden langer duren, en dan laten we je dat binnen de eerste maand weten',
				'Woon je in Argentinië, dan antwoorden we binnen 5 werkdagen en verwijderen we het account binnen 5 werkdagen nadat je hebt aangetoond dat het van jou is (Ley 25.326, art. 16)',
				'Zodra je hebt aangetoond dat het account van jou is, verwijderen we het uiterlijk binnen een maand',
				'Een ouder of voogd kan hetzelfde vragen voor het account van zijn of haar kind'
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
				'Your Google account</strong> is not touched',
				'Server logs are kept for 7 days in our log system',
				'overwritten after a few tens of megabytes',
				'we do not keep backups of the database at the moment, so a deleted account cannot be restored from a copy',
				'until the database reuses that space'
			],
			es: [
				'Lo que ya está en una sala en curso',
				'Los registros de seguridad',
				'se crea una cuenta nueva y vacía',
				'no se puede deshacer',
				'no los borra, y desinstalar la app de Android tampoco',
				'Tu cuenta de Google</strong> no se toca',
				'Los registros del servidor se conservan 7 días en nuestro sistema de registros',
				'se sobrescriben después de unas decenas de megabytes',
				'por ahora no guardamos copias de seguridad de la base de datos, así que una cuenta eliminada no se puede recuperar desde una copia',
				'hasta que la base de datos reutiliza ese espacio'
			],
			nl: [
				'Wat al in een lopende kamer staat',
				'Veiligheidsgegevens',
				'ontstaat er een nieuw, leeg account',
				'niet ongedaan worden gemaakt',
				'wist ze niet, en de Android-app verwijderen ook niet',
				'Je Google-account</strong> blijft onaangeroerd',
				'Serverlogs worden 7 dagen bewaard in ons logsysteem',
				'worden overschreven na enkele tientallen megabytes',
				'we maken op dit moment geen back-ups van de database, dus een verwijderd account kan niet uit een kopie worden teruggezet',
				'tot de database die ruimte opnieuw gebruikt'
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
