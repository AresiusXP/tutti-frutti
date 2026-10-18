// Single source of truth for the Paraglide compiler options, shared by
// vite.config.ts (dev/build) and scripts/compile-i18n.mjs (check/test).
// Strategy: the user's persisted choice (localStorage), otherwise the browser's
// preferred language (es* -> es, en* -> en), otherwise the base locale. Spanish
// (es-AR) is the fallback for any other browser language. An unknown stored
// value is ignored and falls through to the browser language.
// There are deliberately no URL prefixes: this is a static SPA.
/** @type {import('@inlang/paraglide-js').CompilerOptions} */
export const paraglideOptions = {
	project: './project.inlang',
	outdir: './src/lib/paraglide',
	strategy: ['localStorage', 'preferredLanguage', 'baseLocale'],
	localStorageKey: 'tf.locale',
	isServer: 'import.meta.env.SSR',
	emitTsDeclarations: true
};
