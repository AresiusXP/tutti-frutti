// Web app manifest of the installable PWA (consumed by vite.config.ts, checked by
// src/lib/pwa.test.ts). Colours: --color-night in src/app.css = <meta name="theme-color"> in
// src/app.html = theme_color/background_color here (a test keeps them in sync), so neither the
// splash screen nor the first paint flashes another colour.
import type { ManifestOptions } from 'vite-plugin-pwa';
import { BRAND } from '../src/lib/brand.ts';
import screenshots from './screenshots.json' with { type: 'json' };

export const THEME_COLOR = '#1a1033';

/** Install-dialog screenshots (scripts/screenshots.json, rendered by `node scripts/screenshots.mjs`); never precached. */
export const SCREENSHOT_SIZE = '540x960';

export const manifest: Partial<ManifestOptions> = {
	id: '/',
	name: BRAND,
	short_name: BRAND,
	description: `${BRAND}: Basta multijugador en tiempo real`,
	lang: 'es',
	dir: 'ltr',
	start_url: '/',
	scope: '/',
	display: 'standalone',
	orientation: 'portrait',
	theme_color: THEME_COLOR,
	background_color: THEME_COLOR,
	categories: ['games'],
	icons: [
		{ src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
		{ src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
		{ src: '/icons/maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' }
	],
	screenshots: screenshots.map(({ name, label }) => ({
		src: `/screenshots/${name}.png`,
		sizes: SCREENSHOT_SIZE,
		type: 'image/png',
		form_factor: 'narrow' as const,
		label
	}))
};
