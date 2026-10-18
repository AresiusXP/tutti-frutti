import { existsSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { manifest, SCREENSHOT_SIZE, THEME_COLOR } from '../../scripts/pwa-manifest';
import { BRAND } from './brand';

const root = fileURLToPath(new URL('../../', import.meta.url));
const read = (file: string) => readFileSync(join(root, file), 'utf8');

/** Width and height from the IHDR chunk of a PNG file. */
function pngSize(file: string): string {
	const data = readFileSync(join(root, file));
	expect(data.subarray(1, 4).toString('latin1'), `${file} is a PNG`).toBe('PNG');
	return `${data.readUInt32BE(16)}x${data.readUInt32BE(20)}`;
}

const staticPath = (src: string) => `static${src}`;

describe('web app manifest', () => {
	it('is installable: identity, scope, display and language', () => {
		expect(manifest).toMatchObject({
			id: '/',
			name: BRAND,
			short_name: BRAND,
			start_url: '/',
			scope: '/',
			display: 'standalone',
			orientation: 'portrait',
			lang: 'es',
			dir: 'ltr'
		});
		expect(manifest.categories).toContain('games');
		expect(manifest.description).toContain(BRAND);
	});

	it('has any + maskable icons whose files exist with the declared size', () => {
		const icons = manifest.icons ?? [];
		for (const size of ['192x192', '512x512']) {
			expect(icons.some((i) => i.sizes === size && i.purpose === 'any')).toBe(true);
		}
		expect(icons.some((i) => i.purpose === 'maskable' && i.sizes === '512x512')).toBe(true);
		for (const icon of icons) {
			expect(icon.type).toBe('image/png');
			expect(pngSize(staticPath(icon.src)), icon.src).toBe(icon.sizes);
		}
	});

	it('lists narrow screenshots of one real size, each small enough, never precached', () => {
		const shots = manifest.screenshots ?? [];
		expect(shots.length).toBeGreaterThanOrEqual(3);
		for (const shot of shots) {
			const file = staticPath(shot.src);
			expect(shot).toMatchObject({
				sizes: SCREENSHOT_SIZE,
				type: 'image/png',
				form_factor: 'narrow'
			});
			expect(shot.label?.length).toBeGreaterThan(5);
			expect(pngSize(file), shot.src).toBe(SCREENSHOT_SIZE);
			expect(statSync(join(root, file)).size, shot.src).toBeLessThanOrEqual(150 * 1024);
		}
		const [width, height] = SCREENSHOT_SIZE.split('x').map(Number);
		expect(height / width).toBeLessThanOrEqual(2.3);
		expect(read('vite.config.ts')).toMatch(/globIgnores:\s*\[[^\]]*'\*\*\/screenshots\/\*\*'/);
	});
});

describe('first paint and platform meta tags', () => {
	const html = read('src/app.html');
	const meta = (name: string) =>
		new RegExp(`<meta\\s+name="${name}"\\s+content="([^"]*)"`).exec(html)?.[1];

	it('uses the same colour for theme-color, the manifest and the app background', () => {
		expect(meta('theme-color')).toBe(THEME_COLOR);
		expect(manifest.theme_color).toBe(THEME_COLOR);
		expect(manifest.background_color).toBe(THEME_COLOR);
		expect(/--color-night:\s*(#[0-9a-f]{6})/i.exec(read('src/app.css'))?.[1]).toBe(THEME_COLOR);
	});

	it('has the iOS home-screen tags, the manifest link and a 180px touch icon', () => {
		expect(meta('apple-mobile-web-app-capable')).toBe('yes');
		expect(meta('mobile-web-app-capable')).toBe('yes');
		expect(meta('apple-mobile-web-app-status-bar-style')).toBeTruthy();
		expect(meta('apple-mobile-web-app-title')).toBe(BRAND);
		expect(html).toMatch(/<link rel="manifest" href="\/manifest\.webmanifest"/);
		expect(html).toMatch(/<link rel="apple-touch-icon" href="\/apple-touch-icon\.png"/);
		expect(pngSize('static/apple-touch-icon.png')).toBe('180x180');
		expect(html).toMatch(/viewport-fit=cover/);
	});
});

describe('service worker policy', () => {
	const config = read('vite.config.ts');

	it('only updates through the prompt: no automatic skipWaiting / clientsClaim', () => {
		expect(config).toMatch(/registerType:\s*'prompt'/);
		expect(config).not.toMatch(/skipWaiting\s*:\s*true/);
		expect(config).not.toMatch(/clientsClaim\s*:\s*true/);
		expect(config).toMatch(/cleanupOutdatedCaches:\s*true/);
	});

	it('never intercepts the backend namespace', () => {
		expect(config).toMatch(
			/navigateFallbackDenylist:\s*\[\/\^\\\/\(\?:api\|ws\|healthz\|readyz\|metrics\)/
		);
		expect(config).toMatch(/handler:\s*'NetworkOnly'/);
	});

	describe.runIf(existsSync(join(root, 'build', 'sw.js')))('the built sw.js', () => {
		const sw = existsSync(join(root, 'build', 'sw.js')) ? read('build/sw.js') : '';

		it('skips waiting only when the page asks for it', () => {
			expect([...sw.matchAll(/skipWaiting\(\)/g)]).toHaveLength(1);
			expect(sw).toMatch(/"SKIP_WAITING"===[^;]*\.type&&self\.skipWaiting\(\)/);
			expect(sw).not.toContain('clientsClaim');
			expect(sw).toContain('cleanupOutdatedCaches');
		});

		it('precaches the shell but neither the backend, nor screenshots, nor itself', () => {
			const urls = [...sw.matchAll(/\{url:"([^"]+)"/g)].map((m) => m[1]);
			expect(urls).toContain('index.html');
			expect(urls).toContain('manifest.webmanifest');
			expect(
				urls.filter((u) => /^(?:api|ws|healthz)|screenshots|^sw\.js|^workbox-/.test(u))
			).toEqual([]);
			expect(sw).toMatch(/denylist:\[\/\^\\\/\(\?:api\|ws\|healthz\|readyz\|metrics\)/);
			expect(sw).toContain('NetworkOnly');
		});
	});
});
