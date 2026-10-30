import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';
import { locales } from './paraglide/runtime';

const root = fileURLToPath(new URL('../../', import.meta.url));
const html = readFileSync(join(root, 'static/privacy.html'), 'utf8');
const css = readFileSync(join(root, 'static/privacy.css'), 'utf8');
const sections = html.split('<section class="doc"').slice(1);

/**
 * static/privacy.html is the policy linked from the Google Cloud consent screen. It is plain HTML
 * (no script), so these tests guard what the page says about the data the app really handles.
 */
describe('static/privacy.html', () => {
	it('has one section per app language, each linked from the language bar', () => {
		expect(sections).toHaveLength(locales.length);
		for (const locale of locales) {
			expect(html).toContain(`<section class="doc" id="${locale}" lang="${locale}">`);
			expect(html).toContain(`<a href="#${locale}" hreflang="${locale}" lang="${locale}">`);
			// the stylesheet switches language with :target
			expect(css).toContain(`#${locale}`);
		}
	});

	it('has the same numbered sections in every language', () => {
		const numbers = sections.map((s) => [...s.matchAll(/<h2>\s*(\d+)\./g)].map((m) => m[1]));
		expect(numbers[0]).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11']);
		for (const n of numbers) expect(n).toEqual(numbers[0]);
	});

	it('states the same facts in every language: contact, cookies, scopes, retention, deletion', () => {
		for (const section of sections) {
			expect(section).toContain('mailto:aresiusxp@gmail.com');
			for (const cookie of ['tf_guest', 'tf_session', 'tf_oauth'])
				expect(section).toContain(cookie);
			expect(section).toContain('<code>openid</code>');
			expect(section).toContain('<code>profile</code>');
			expect(section).toContain(
				'https://developers.google.com/terms/api-services-user-data-policy'
			);
			expect(section).toContain('https://policies.google.com/privacy');
			// the optional AI judge sends the answers of a category to Cloudflare Workers AI
			expect(section).toContain('https://www.cloudflare.com/privacypolicy/');
			expect(section).toContain('Cloudflare');
			expect(section).toContain('24');
			expect(section).toContain('https://myaccount.google.com/permissions');
			// 30 days (session cookie and the answer time), 10 minutes (OAuth), 6 hours (cleanup), 7 days (logs)
			for (const figure of ['30', '10', '6', '7'])
				expect(section).toMatch(new RegExp(`\\b${figure}\\b`));
		}
	});

	it('explains public discovery and casual statistics in every language', () => {
		for (const [locale, discovery, customs, privateRooms, casual] of [
			[
				'en',
				'Public room discovery',
				'Custom category names are not shown there',
				'Private rooms are not listed',
				'lifetime statistics are not recorded'
			],
			[
				'es',
				'buscador de salas públicas',
				'Los nombres de las categorías propias no se muestran ahí',
				'Las salas privadas no aparecen',
				'no se guardan estadísticas'
			],
			[
				'nl',
				'lijst met openbare kamers',
				'Eigen categorienamen worden daar niet getoond',
				'Privékamers staan niet in deze lijst',
				'levenslange statistieken worden niet bijgehouden'
			]
		]) {
			const section = sections.find((s) => s.includes(`id="${locale}"`))!.replace(/\s+/g, ' ');
			for (const fact of [discovery, customs, privateRooms, casual])
				expect(section).toContain(fact);
		}
	});

	it('says what the browser keeps to resume the last game (tf.recentRoom) in every language', () => {
		// the code and a random technical ID only, forgotten after 24 hours or on leaving: see "Resume recent game"
		for (const [locale, facts] of [
			[
				'en',
				[
					'the code and a random technical ID of the last game you joined',
					'no names and no answers',
					'forgotten after 24 hours or when you leave that game'
				]
			],
			[
				'es',
				[
					'el código y un ID técnico aleatorio de la última partida a la que entraste',
					'sin nombres ni respuestas',
					'se olvida pasadas 24 horas o cuando salís de esa partida'
				]
			],
			[
				'nl',
				[
					'de code en een willekeurig technisch ID van het laatste spel waaraan je deelnam',
					'geen namen en geen antwoorden',
					'na 24 uur of zodra je dat spel verlaat vergeten'
				]
			]
		] as const) {
			const section = sections.find((s) => s.includes(`id="${locale}"`))!.replace(/\s+/g, ' ');
			for (const fact of facts) expect(section, `${locale}: ${fact}`).toContain(fact);
		}
	});

	it('describes the safety records, the Android app and the real lifetimes in every language', () => {
		const facts: Record<string, string[]> = {
			en: [
				'your random guest ID, the version of the terms, the time, the language of the screen',
				'confirm that you are 13 or older',
				'kept for up to 365 days',
				'comment of up to 280 characters',
				'We do not share it with anybody, except with authorities when the law requires it',
				'kept until 30 days after it is resolved and, in any case, for at most 90 days',
				'The record is kept until you unblock them.',
				'restriction for as long as it lasts plus 90 days, and a log of what we did and why for one year',
				'does not replace any rule about age or permission',
				'is a thin container that opens the website',
				'Uninstalling the app does not delete them, and it does not delete your account',
				'Google Play receives whatever Google itself collects',
				'the answers that all players typed in that category',
				'when nobody has been connected for 30 minutes, when nobody joined it within 2 minutes',
				'can exist for longer than 6 hours',
				'may stay in memory until it is evicted or the server restarts',
				'uses your Google display name as your nickname',
				'we aim to keep them for about 7 days'
			],
			es: [
				'tu ID de invitado aleatorio, la versión de los términos, la hora, el idioma de la pantalla',
				'confirmar que tenés 13 años o más',
				'Se conserva hasta 365 días',
				'comentario opcional de hasta 280 caracteres',
				'No lo compartimos con nadie, salvo con las autoridades cuando la ley lo exige',
				'se conserva hasta 30 días después de que se resuelve y, en todo caso, como máximo 90 días',
				'El registro se conserva hasta que lo desbloqueés.',
				'registro de la restricción, mientras dure y 90 días más, y un registro de lo que hicimos y por qué durante un año',
				'no reemplaza ninguna regla sobre edad o permisos',
				'es un contenedor liviano que abre el sitio web',
				'Desinstalar la app no los borra, y tampoco borra tu cuenta',
				'Google Play recibe lo que Google mismo recolecta',
				'todos los jugadores',
				'cuando nadie estuvo conectado durante 30 minutos, cuando nadie entró en los 2 minutos',
				'pueden existir más de 6 horas',
				'puede quedar en memoria hasta que se la desaloje o se reinicie el servidor',
				'usa tu nombre visible en Google como apodo',
				'buscamos conservarlos unos 7 días'
			],
			nl: [
				'je willekeurige gast-ID, de versie van de voorwaarden, het tijdstip, de taal van het scherm',
				'bevestigen dat je 13 jaar of ouder bent',
				'maximaal 365 dagen bewaard',
				'opmerking van maximaal 280 tekens',
				'We delen het met niemand, behalve met autoriteiten als de wet dat eist',
				'bewaard tot 30 dagen nadat ze is afgehandeld en in elk geval maximaal 90 dagen',
				'blijft bewaard tot je de blokkade opheft.',
				'gegeven over de beperking zolang die duurt plus 90 dagen, en een logboek van wat we hebben gedaan en waarom gedurende een jaar',
				'vervangt geen regel over leeftijd of toestemming',
				'is een dunne verpakking die de website opent',
				'De app verwijderen wist ze niet, en wist ook je account',
				'Google Play ontvangt wat Google zelf verzamelt',
				'alle spelers',
				'als er 30 minuten niemand verbonden is geweest, als niemand binnen 2 minuten',
				'langer dan 6 uur bestaan',
				'kan anders in het geheugen blijven staan tot het wordt verdrongen of de server herstart',
				'gebruikt het spel je Google-weergavenaam als bijnaam',
				'streven ernaar ze ongeveer 7 dagen te bewaren'
			]
		};
		for (const section of sections) {
			const locale = /id="(\w+)"/.exec(section)![1];
			const text = section.replace(/<[^>]*>/g, '').replace(/\s+/g, ' ');
			for (const fact of facts[locale]) expect(text, `${locale}: ${fact}`).toContain(fact);
			expect(section).toContain(`href="/terms#${locale}"`);
			expect(section).toContain(`href="/delete-account#${locale}"`);
		}
	});

	it('no longer claims what the code does not do', () => {
		const never: Record<string, RegExp> = {
			// the Google name becomes the first nickname, so other players can see it
			en: /never to other players|never sent to us|anything older than 6 hours|and after 24 hours at most/i,
			es: /nunca a otros jugadores|nunca se nos envía|todo lo que tenga más de\s+6 horas|a las 24 horas/i,
			nl: /nooit aan andere spelers|nooit naar ons gestuurd|alles wat ouder is\s+dan 6 uur|na hooguit 24 uur/i
		};
		for (const section of sections) {
			const locale = /id="(\w+)"/.exec(section)![1];
			expect(section.replace(/\s+/g, ' '), locale).not.toMatch(never[locale]);
		}
	});

	it('writes the brand exactly like the constant, and names the app in the title', () => {
		for (const [found] of html.matchAll(/tutti\s+frutti/gi)) expect(found).toBe(BRAND);
		expect(html).toMatch(new RegExp(`<title>[^<]*${BRAND}</title>`));
	});

	it('never claims what the app does not do: no email, no picture, no analytics scripts', () => {
		// the Google scopes the server asks for (server/internal/auth/google.go): openid + profile only
		expect(html).not.toMatch(/\bemail\b[^<]*scope|scope[^<]*\bemail\b/i);
		expect(html).not.toMatch(/googletagmanager|google-analytics|gtag\(|fbq\(|hotjar/i);
	});
});
