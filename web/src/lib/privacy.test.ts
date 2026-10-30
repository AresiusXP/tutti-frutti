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
			// 30 days (session cookie), 10 minutes (OAuth), 6 hours (cleanup), 7 days (logs)
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
				'uses your Google display name as your nickname'
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
				'usa tu nombre visible en Google como apodo'
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
				'gebruikt het spel je Google-weergavenaam als bijnaam'
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

	it('states logs, backups, legal bases, deadlines, authorities and minors in every language', () => {
		const facts: Record<string, string[]> = {
			en: [
				// logs: no player IP address reaches the game, Cloudflare (the network in front) handles it
				'connections reach our servers only from internal network addresses',
				'The game does not read, log or store your IP address',
				'Cloudflare itself does process connection data, including your IP address, to deliver the site to you',
				'The game server does not log request headers',
				'Server logs are kept for 7 days in our log system',
				'short-lived rotating copies, which are overwritten after a few tens of megabytes',
				'Cloudflare, on every visit',
				// no backups
				'we do not keep backups of the database at the moment, so deleted data cannot be restored from a copy',
				'until the database reuses that space',
				'If we ever introduce backups, this policy will first say how long they are kept',
				// legal bases, never consent
				'Art. 6(1)(b) GDPR; in Argentina, art. 5.2(d) of Ley 25.326',
				'legitimate interest in a safe and fair game (Art. 6(1)(f) GDPR)',
				'particular care of players under 18',
				'We do not rely on your consent.',
				// deadlines and complaints
				'We answer without undue delay, at the latest within one month, or sooner where the law where you live requires it',
				'if you live in Argentina, within 10 days for access to your data and within 5 business days for correction or deletion (Ley 25.326, arts. 14 and 16)',
				'Where the law allows it (not in Argentina), a complex request can take two more months; if so, we tell you within the first month',
				'complain to a data protection authority',
				// minors
				'16 in the Netherlands',
				'Younger players can play as guests, or sign in with a supervised Google Account if a parent allows it',
				"A parent or guardian can ask us by email to delete their child's data; we verify the request first",
				// the support link
				'on the website only (not in the Android app)',
				'Supporting the game is voluntary and gives nothing in return',
				'(Cafecito, Ko-fi) have their own terms and privacy policies and only accept supporters aged 18 or older'
			],
			es: [
				'las conexiones llegan a nuestros servidores solo desde direcciones internas de la red',
				'El juego no lee, no registra ni guarda tu dirección IP',
				'Cloudflare sí procesa datos de conexión, incluida tu dirección IP, para hacerte llegar el sitio',
				'El servidor del juego no registra los encabezados de los pedidos',
				'Los registros del servidor se conservan 7 días en nuestro sistema de registros',
				'copias rotativas de corta duración, que se sobrescriben después de unas decenas de megabytes',
				'Cloudflare, en cada visita',
				'por ahora no guardamos copias de seguridad de la base de datos, así que lo que se borra no se puede recuperar desde una copia',
				'hasta que la base de datos reutiliza ese espacio',
				'Si alguna vez agregamos copias de seguridad, esta política va a decir antes cuánto tiempo se guardan',
				'art. 6(1)(b) del RGPD; en Argentina, art. 5.2(d) de la Ley 25.326',
				'interés legítimo en un juego seguro y justo (art. 6(1)(f) del RGPD)',
				'cuidamos especialmente a los jugadores menores de 18 años',
				'No nos basamos en tu consentimiento.',
				'Respondemos sin demoras indebidas, como máximo dentro de un mes, o antes si la ley del lugar donde vivís lo exige',
				'si vivís en Argentina, dentro de los 10 días para acceder a tus datos y dentro de los 5 días hábiles para corregirlos o eliminarlos (Ley 25.326, arts. 14 y 16)',
				'Donde la ley lo permite (no en Argentina), un pedido complejo puede llevar dos meses más; en ese caso te avisamos dentro del primer mes',
				'presentar un reclamo ante una autoridad de protección de datos',
				'16 años en los Países Bajos',
				'Los jugadores más chicos pueden jugar como invitados, o iniciar sesión con una cuenta de Google supervisada',
				'puede pedirnos por correo que eliminemos los datos de su hijo o hija; primero verificamos el pedido',
				'solo en el sitio web (no en la app de Android)',
				'Apoyar el juego es voluntario y no da nada a cambio',
				'(Cafecito, Ko-fi) tienen sus propios términos y políticas de privacidad y solo aceptan a personas de 18 años o más'
			],
			nl: [
				'verbindingen bereiken onze servers alleen vanaf interne netwerkadressen',
				'Het spel leest, logt en bewaart je IP-adres niet',
				'Cloudflare verwerkt zelf wel verbindingsgegevens, waaronder je IP-adres, om de site bij je te bezorgen',
				'De spelserver logt geen headers van verzoeken',
				'Serverlogs worden 7 dagen bewaard in ons logsysteem',
				'kortlevende, roterende kopieën, die worden overschreven na enkele tientallen megabytes',
				'Cloudflare, bij elk bezoek',
				'we maken op dit moment geen back-ups van de database, dus verwijderde gegevens kunnen niet uit een kopie worden teruggezet',
				'tot de database die ruimte opnieuw gebruikt',
				'Als we ooit back-ups gaan maken, zegt dit beleid eerst hoe lang die worden bewaard',
				'art. 6(1)(b) AVG; in Argentinië art. 5.2(d) van Ley 25.326',
				'gerechtvaardigd belang bij een veilig en eerlijk spel (art. 6(1)(f) AVG)',
				'letten we extra goed op spelers onder de 18',
				'We baseren ons niet op je toestemming.',
				'We antwoorden zonder onnodige vertraging, uiterlijk binnen een maand, of eerder als de wet van de plaats waar je woont dat eist',
				'woon je in Argentinië, dan binnen 10 dagen voor inzage in je gegevens en binnen 5 werkdagen voor correctie of verwijdering (Ley 25.326, art. 14 en 16)',
				'Waar de wet het toelaat (niet in Argentinië), kan een ingewikkeld verzoek twee maanden langer duren; dan laten we je dat binnen de eerste maand weten',
				'een klacht indienen bij een toezichthouder voor gegevensbescherming',
				'16 jaar in Nederland',
				'Jongere spelers kunnen als gast spelen, of inloggen met een Google-account onder toezicht als een ouder dat toestaat',
				'Een ouder of voogd kan ons per e-mail vragen de gegevens van zijn of haar kind te verwijderen; we controleren het verzoek eerst',
				'alleen op de website (niet in de Android-app)',
				'Het spel steunen is vrijwillig en je krijgt er niets voor terug',
				'(Cafecito, Ko-fi) hebben hun eigen voorwaarden en privacyverklaringen en accepteren alleen mensen van 18 jaar of ouder'
			]
		};
		for (const section of sections) {
			const locale = /id="(\w+)"/.exec(section)![1];
			const text = section.replace(/<[^>]*>/g, '').replace(/\s+/g, ' ');
			for (const fact of facts[locale]) expect(text, `${locale}: ${fact}`).toContain(fact);
			// the two supervisory authorities, linked in every language
			expect(section).toContain('href="https://www.autoriteitpersoonsgegevens.nl"');
			expect(section).toContain('href="https://www.argentina.gob.ar/aaip"');
			expect(text).toContain('Autoriteit Persoonsgegevens');
			expect(text).toContain('Agencia de Acceso a la Información Pública (AAIP)');
		}
	});

	it('no longer claims what the code does not do', () => {
		const never: Record<string, RegExp> = {
			// the Google name becomes the first nickname, so other players can see it; the IP address never
			// reaches the game (Cloudflare Tunnel), and no lawful basis rests on consent
			en: /never to other players|never sent to us|anything older than 6 hours|and after 24 hours at most|keeps your IP address|because you chose to use it|within 30 days/i,
			es: /nunca a otros jugadores|nunca se nos envía|todo lo que tenga más de\s+6 horas|a las 24 horas|guarda tu dirección IP en su memoria|porque elegiste usarlo|dentro de los 30 días/i,
			nl: /nooit aan andere spelers|nooit naar ons gestuurd|alles wat ouder is\s+dan 6 uur|na hooguit 24 uur|houdt hij je IP-adres|omdat je ervoor hebt gekozen|binnen 30 dagen/i
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
