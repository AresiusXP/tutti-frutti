import {
	getLocale,
	isLocale,
	locales,
	overwriteGetLocale,
	setLocale,
	type Locale
} from '$lib/paraglide/runtime';

/**
 * Reactive locale for the SPA. Paraglide resolves the initial locale with its
 * strategy (localStorage -> browser language -> base locale `es`); afterwards
 * `getLocale()` reads this rune, so every `m.*()` call inside a template or
 * `$derived` re-renders when the language changes. No page reload needed.
 */
let current = $state<Locale>(getLocale());
overwriteGetLocale(() => current);

export const LOCALE_LABELS: Record<Locale, string> = { es: 'ES', en: 'EN' };

export const i18n = {
	get locale(): Locale {
		return current;
	},
	get locales(): readonly Locale[] {
		return locales;
	},
	/** Switches language, persists the choice (Paraglide's localStorage strategy) and updates <html lang>. */
	set(next: string) {
		if (!isLocale(next)) return;
		setLocale(next, { reload: false });
		current = next;
		if (typeof document !== 'undefined') document.documentElement.lang = next;
	}
};
