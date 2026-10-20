/**
 * Where the "support the game" link goes. One rule per region, decided in the browser from the device's
 * timezone and language list: nothing is sent to the server and there is no IP lookup (docs/DESIGN.md,
 * "Support links"). To send another region somewhere else (e.g. the Netherlands to a bunq.me link) add a
 * rule to `REGIONS`; the first match wins and `SUPPORT_DEFAULT` is the fallback.
 */

/** Argentina: Cafecito pays out through Mercado Pago, which is what people there use. */
export const SUPPORT_ARGENTINA = 'https://cafecito.app/aresiusxp';
/** Everyone else: Ko-fi (cards, Apple/Google Pay, PayPal). */
export const SUPPORT_DEFAULT = 'https://ko-fi.com/aresiusxp';

/** What the browser says about where the player is. */
export interface SupportEnv {
	/** IANA timezone of the device, e.g. `America/Argentina/Buenos_Aires`. */
	timeZone: string | undefined;
	/** BCP 47 language tags in order of preference, e.g. `es-AR`. */
	languages: readonly string[];
}

interface Region {
	url: string;
	matches(env: SupportEnv): boolean;
}

/** Every IANA zone of Argentina: `America/Argentina/*` plus the legacy aliases still reported by some devices. */
const ARGENTINA_ZONE =
	/^America\/(?:Argentina\/[A-Za-z_]+|Buenos_Aires|Catamarca|Cordoba|Jujuy|Mendoza|Rosario)$/;
/**
 * A language tag whose region is Argentina (`es-AR`, `en-AR`, `es-Latn-AR`, `es-AR-u-ca-gregory`): read by
 * `Intl.Locale`, with this pattern (language, optional extended language and script, then `AR`) for a tag it
 * refuses.
 */
const ARGENTINA_TAG = /^[a-z]{2,8}(?:-[a-z]{3}){0,3}(?:-[a-z]{4})?-ar(?:-|$)/i;

function isArgentineTag(tag: string): boolean {
	try {
		if (new Intl.Locale(tag).region === 'AR') return true;
	} catch {
		// not a tag Intl understands: the pattern below decides
	}
	return ARGENTINA_TAG.test(tag);
}

const REGIONS: readonly Region[] = [
	{
		url: SUPPORT_ARGENTINA,
		matches: ({ timeZone, languages }) =>
			(timeZone !== undefined && ARGENTINA_ZONE.test(timeZone)) || languages.some(isArgentineTag)
	}
];

/** The link for a player in this environment: pure, so every case is a plain test. */
export function supportUrl(env: SupportEnv): string {
	return REGIONS.find((region) => region.matches(env))?.url ?? SUPPORT_DEFAULT;
}

/** The environment of this browser. Anything the browser refuses to say is left out (never throws). */
export function detectSupportEnv(): SupportEnv {
	let timeZone: string | undefined;
	try {
		timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
	} catch {
		// no Intl timezone: the language list may still tell
	}
	let languages: readonly string[] = [];
	try {
		languages = navigator.languages?.length ? navigator.languages : [navigator.language];
	} catch {
		// no navigator (tests, SSR): nothing to go on
	}
	return { timeZone, languages: languages.filter((tag) => typeof tag === 'string') };
}

/** The support link for the person looking at the page right now. */
export const supportLink = (): string => supportUrl(detectSupportEnv());
