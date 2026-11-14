import adapterStatic from '@sveltejs/adapter-static';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { paraglideVitePlugin } from '@inlang/paraglide-js';
import type { Adapter } from '@sveltejs/kit';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { withPwa } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vitest/config';
import { paraglideOptions } from './paraglide.config.js';
import { writeCspHashes } from './scripts/csp.mjs';
import { manifest } from './scripts/pwa-manifest.ts';

const isTest = Boolean(process.env.VITEST);

// Go server (see server/). Override when it runs elsewhere.
const serverUrl = process.env.TF_SERVER_URL ?? 'http://localhost:8080';
const proxy = {
	'/api': { target: serverUrl, changeOrigin: true },
	'/ws': { target: serverUrl, changeOrigin: true, ws: true }
};

const PAGES = 'build';

/**
 * adapter-static, then the Content-Security-Policy hashes (scripts/csp.mjs): they are derived
 * from the generated build (index.html + client bundle) into `.svelte-kit/nginx/csp-hashes.conf`, which the
 * Dockerfile ships next to nginx-security-headers.conf. The build fails when the shell contains
 * inline code the policy would block.
 */
function withCsp(inner: Adapter): Adapter {
	return {
		...inner,
		async adapt(builder) {
			await inner.adapt(builder);
			writeCspHashes({
				buildDir: PAGES,
				outPath: join(builder.getBuildDirectory('nginx'), 'csp-hashes.conf')
			});
		}
	};
}

const kit: NonNullable<Parameters<typeof sveltekit>[0]> = {
	// Pure client-side SPA served as static files; unknown paths (e.g. /KT7P
	// invite links) fall back to index.html.
	adapter: withCsp(
		adapterStatic({
			pages: PAGES,
			assets: PAGES,
			fallback: 'index.html',
			precompress: false
		})
	),
	// Absolute asset URLs so the SPA fallback works on deep links like /KT7P.
	paths: { relative: false }
};

export default defineConfig({
	plugins: [
		tailwindcss(),
		...(isTest
			? [sveltekit(kit), svelteTesting()]
			: [
					paraglideVitePlugin(paraglideOptions),
					withPwa(kit, {
						registerType: 'prompt',
						strategies: 'generateSW',
						kit: { adapterFallback: 'index.html', spa: true },
						manifest,
						generateSW: {
							// @vite-pwa/sveltekit always adds `prerendered/**/*.{html,json}`: an SPA prerenders no pages and
							// the strict glob check (production) fails on a pattern that matches nothing
							globStrict: false,
							cleanupOutdatedCaches: true,
							// The store screenshots are only fetched by the install dialog, and the
							// link-preview image only by chat crawlers: keep them out of the precache
							// (every client would download them for nothing).
							globIgnores: [
								'**/screenshots/**',
								'**/og-image.png',
								'**/node_modules/**/*',
								'sw.js',
								'workbox-*.js',
								// the privacy policy is a static page outside the app, always fetched fresh
								'**/privacy.html',
								'**/privacy.css',
								// the terms and the account-deletion page are static pages like it
								'**/terms.html',
								'**/delete-account.html',
								// Digital Asset Links of the Android app: fetched by Google, not part of the app
								'**/.well-known/**'
							],
							// Backend routes are served by the Go server: never answer them
							// with index.html nor from a cache.
							navigateFallbackDenylist: [
								/^\/(?:api|ws|healthz|readyz|metrics)(?:\/|$)/,
								// a real page of its own, not an SPA route: let the network answer it
								/^\/privacy(?:\/|\.html)?$/,
								/^\/(?:terms|delete-account)(?:\/|\.html)?$/,
								// Digital Asset Links (/.well-known/assetlinks.json): a file, never the app shell
								/^\/\.well-known(?:\/|$)/
							],
							runtimeCaching: [
								{
									// Workbox matches a RegExp against the whole URL (same origin: from its start)
									urlPattern: /^https?:\/\/[^/]+\/(?:api|ws|healthz|readyz|metrics)(?:[/?#]|$)/,
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
		setupFiles: ['./src/test.setup.ts'],
		environment: 'node',
		// the PWA plugin (and with it `virtual:pwa-register`) is not loaded under Vitest
		alias: {
			'virtual:pwa-register': fileURLToPath(
				new URL('./src/lib/pwa/register.stub.ts', import.meta.url)
			)
		}
	}
});
