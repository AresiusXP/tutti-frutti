import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// Pure client-side SPA served as static files; unknown paths (e.g. /KT7P
		// invite links) fall back to index.html.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: 'index.html',
			precompress: false
		}),
		// Absolute asset URLs so the SPA fallback works on deep links like /KT7P.
		paths: { relative: false },
		// The PWA plugin registers the service worker via `virtual:pwa-register`.
		serviceWorker: { register: false }
	}
};

export default config;
