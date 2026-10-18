import adapterStatic from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { join } from 'node:path';
import { writeCspHashes } from './scripts/csp.mjs';

const PAGES = 'build';

/**
 * adapter-static, then the Content-Security-Policy hashes (scripts/csp.mjs): they are derived
 * from the generated build (index.html + client bundle) into `.svelte-kit/nginx/csp-hashes.conf`, which the
 * Dockerfile ships next to nginx-security-headers.conf. The build fails when the shell contains
 * inline code the policy would block.
 * @param {import('@sveltejs/kit').Adapter} inner
 * @returns {import('@sveltejs/kit').Adapter}
 */
function withCsp(inner) {
	return {
		...inner,
		async adapt(builder) {
			await inner.adapt(builder);
			writeCspHashes({
				buildDir: PAGES,
				outPath: join(builder.config.kit.outDir, 'nginx', 'csp-hashes.conf')
			});
		}
	};
}

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
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
		paths: { relative: false },
		// The PWA plugin registers the service worker via `virtual:pwa-register`.
		serviceWorker: { register: false }
	}
};

export default config;
