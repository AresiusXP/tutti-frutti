import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vitest/config';
import { paraglideOptions } from './paraglide.config.js';
import { BRAND } from './src/lib/brand.ts';

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
						manifest: {
							id: '/',
							name: BRAND,
							short_name: BRAND,
							description: `${BRAND}: Basta multijugador en tiempo real`,
							lang: 'es',
							start_url: '/',
							scope: '/',
							display: 'standalone',
							orientation: 'portrait',
							theme_color: '#1a1033',
							background_color: '#1a1033',
							categories: ['games'],
							icons: [
								{ src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
								{ src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
								{
									src: '/icons/maskable-512.png',
									sizes: '512x512',
									type: 'image/png',
									purpose: 'maskable'
								}
							]
						},
						workbox: {
							cleanupOutdatedCaches: true,
							// Backend routes are served by the Go server: never answer them
							// with index.html nor from a cache.
							navigateFallbackDenylist: [/^\/(?:api|ws|healthz|readyz|metrics)(?:\/|$)/],
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
