import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vitest/config';
import { paraglideOptions } from './paraglide.config.js';
import { manifest } from './scripts/pwa-manifest.ts';

const isTest = Boolean(process.env.VITEST);

// Go server (see server/). Override when it runs elsewhere.
const serverUrl = process.env.TF_SERVER_URL ?? 'http://localhost:8080';
const proxy = {
	'/api': { target: serverUrl, changeOrigin: true },
	'/ws': { target: serverUrl, changeOrigin: true, ws: true }
};

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit(),
		...(isTest
			? [svelteTesting()]
			: [
					paraglideVitePlugin(paraglideOptions),
					SvelteKitPWA({
						registerType: 'prompt',
						strategies: 'generateSW',
						kit: { adapterFallback: 'index.html', spa: true },
						manifest,
						workbox: {
							cleanupOutdatedCaches: true,
							// The store screenshots are only fetched by the install dialog: keep them
							// out of the precache (every client would download them for nothing).
							globIgnores: [
								'**/screenshots/**',
								'**/node_modules/**/*',
								'sw.js',
								'workbox-*.js',
								// the privacy policy is a static page outside the app, always fetched fresh
								'**/privacy.html',
								'**/privacy.css'
							],
							// Backend routes are served by the Go server: never answer them
							// with index.html nor from a cache.
							navigateFallbackDenylist: [
								/^\/(?:api|ws|healthz|readyz|metrics)(?:\/|$)/,
								// a real page of its own, not an SPA route: let the network answer it
								/^\/privacy(?:\/|\.html)?$/
							],
							runtimeCaching: [
								{
									// must be self-contained: workbox serialises it into sw.js
									urlPattern: ({ url }: { url: URL }) =>
										/^\/(?:api|ws|healthz|readyz|metrics)(?:\/|$)/.test(url.pathname),
									handler: 'NetworkOnly'
								}
							]
						},
						devOptions: { enabled: false }
					})
				])
	],
	server: { proxy },
	preview: { proxy },
	test: {
		include: ['src/**/*.{test,spec}.ts'],
		environment: 'node'
	}
});
