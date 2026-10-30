/**
 * Is this page running inside the Android app (the Trusted Web Activity shell, package `xyz.aresius.tuttifrutti`)?
 * It only decides what the page SHOWS (the Cafecito / Ko-fi links are hidden there, see docs/DESIGN.md "Support
 * links" and docs/ANDROID.md): never authorization, never sent to the server, never read from `localStorage`
 * (the TWA and the website share the browser's localStorage, so a flag there would hide the links on the website too).
 *
 * The signal: a TWA launch loads its first document with `document.referrer` set to `android-app://<package>`.
 * `startAndroidContext()` reads it once, when the app starts (the root layout), and keeps a per-tab marker in
 * `sessionStorage`. That marker is what `isAndroidApp()` reads, so it holds for every later screen:
 *
 *  - same-tab navigations (SvelteKit routes, `goto`) never reload the document and keep the marker;
 *  - coming back from Google sign-in (`accounts.google.com`, then the callback) or from the static pages
 *    (`/privacy`, `/terms`, `/delete-account`, `rel="external"`, same tab) is a new document whose referrer is not
 *    `android-app://` any more, but `sessionStorage` is per tab and origin and survives it, and nothing ever CLEARS
 *    the marker because the referrer differs;
 *  - a second conservative signal: the page's referrer names this package AND it runs in a standalone / fullscreen
 *    display mode (so it still counts when the marker could not be written). A plain installed PWA launched from the
 *    website has no such referrer and keeps the links; another app that opens the PWA (`android-app://com.whatsapp`)
 *    is not this package and does not count either.
 *
 * Residual risk (documented in docs/ANDROID.md): a document opened in ANOTHER tab or browsing context without the
 * referrer (a Chrome Custom Tab that re-opens the site for an external link, a restored task whose session was lost)
 * has no marker, and then the links show. That is the only way they can appear in the app.
 */

export const ANDROID_PACKAGE = 'xyz.aresius.tuttifrutti';
/** What `document.referrer` starts with on a TWA launch: `android-app://<package>`. */
export const ANDROID_REFERRER = `android-app://${ANDROID_PACKAGE}`;
/** sessionStorage key of the per-tab marker (presentation state only). */
export const ANDROID_MARKER_KEY = 'tf.android';

type SessionLike = Pick<Storage, 'getItem' | 'setItem'>;

/** Everything the functions read from the browser (injected in tests). */
export interface AndroidEnv {
	/** `document.referrer` */
	referrer: string;
	/** `sessionStorage`, or `null` when the browser refuses it. */
	session: SessionLike | null;
	/** True when the page runs in `(display-mode: standalone)` or `fullscreen`. Evaluated only when needed. */
	appDisplayMode: () => boolean;
}

/**
 * True when `referrer` is exactly this package: `android-app://xyz.aresius.tuttifrutti`, optionally followed by a
 * path (`/…`). A look-alike such as `android-app://xyz.aresius.tuttifrutti.evil` is not this package.
 */
export function isAndroidReferrer(referrer: string | null | undefined): boolean {
	if (typeof referrer !== 'string' || !referrer.startsWith(ANDROID_REFERRER)) return false;
	return referrer.length === ANDROID_REFERRER.length || referrer[ANDROID_REFERRER.length] === '/';
}

function attempt<T>(read: () => T, fallback: T): T {
	try {
		return read();
	} catch {
		return fallback;
	}
}

function browserEnv(): AndroidEnv {
	return {
		referrer: attempt(() => (typeof document === 'undefined' ? '' : document.referrer), ''),
		session: attempt(() => (typeof sessionStorage === 'undefined' ? null : sessionStorage), null),
		appDisplayMode: () =>
			attempt(
				() =>
					['standalone', 'fullscreen'].some(
						(mode) => window.matchMedia(`(display-mode: ${mode})`).matches
					),
				false
			)
	};
}

/**
 * Call once when the app starts. A TWA launch (the referrer names this package) puts the marker in this tab's
 * sessionStorage. Any other referrer does nothing: it never removes an existing marker.
 */
export function startAndroidContext(env: AndroidEnv = browserEnv()): void {
	if (!isAndroidReferrer(env.referrer)) return;
	try {
		env.session?.setItem(ANDROID_MARKER_KEY, '1');
	} catch {
		// storage refused: the second signal in isAndroidApp() may still say so
	}
}

/** True inside the Android app: this tab carries the marker, or the page was launched by the app in an app display mode. */
export function isAndroidApp(env: AndroidEnv = browserEnv()): boolean {
	if (attempt(() => env.session?.getItem(ANDROID_MARKER_KEY) === '1', false)) return true;
	return isAndroidReferrer(env.referrer) && attempt(env.appDisplayMode, false);
}
