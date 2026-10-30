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

	it('never answers /.well-known/ (Digital Asset Links) with the app shell, and never precaches it', () => {
		expect(config).toMatch(
			/navigateFallbackDenylist:\s*\[[^\]]*\/\^\\\/\\\.well-known\(\?:\\\/\|\$\)\//
		);
		expect(config).toMatch(/globIgnores:\s*\[[^\]]*'\*\*\/\.well-known\/\*\*'/);
	});

	it('never intercepts the backend namespace', () => {
		expect(config).toMatch(
			/navigateFallbackDenylist:\s*\[\s*\/\^\\\/\(\?:api\|ws\|healthz\|readyz\|metrics\)/
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

		it('precaches the shell but neither the backend, nor screenshots, nor the preview image, nor itself', () => {
			const urls = [...sw.matchAll(/\{url:"([^"]+)"/g)].map((m) => m[1]);
			expect(urls).toContain('index.html');
			expect(urls).toContain('manifest.webmanifest');
			expect(
				urls.filter((u) => /^(?:api|ws|healthz)|screenshots|og-image|^sw\.js|^workbox-/.test(u))
			).toEqual([]);
			expect(sw).toMatch(/denylist:\[\/\^\\\/\(\?:api\|ws\|healthz\|readyz\|metrics\)/);
			expect(sw).toContain('NetworkOnly');
		});

		it('leaves /.well-known/ to the network: not precached, not an SPA fallback', () => {
			const urls = [...sw.matchAll(/\{url:"([^"]+)"/g)].map((m) => m[1]);
			expect(urls.filter((u) => /well-known|assetlinks/.test(u))).toEqual([]);
			expect(sw).toMatch(/denylist:\[[^\]]*\/\^\\\/\\\.well-known\(\?:\\\/\|\$\)\//);
		});

		it('leaves the static privacy policy to the network: not precached, not an SPA fallback', () => {
			const urls = [...sw.matchAll(/\{url:"([^"]+)"/g)].map((m) => m[1]);
			expect(urls.filter((u) => /^privacy/.test(u))).toEqual([]);
			expect(sw).toMatch(/denylist:\[[^\]]*\/\^\\\/privacy\(\?:\\\/\|\\\.html\)\?\$\//);
		});

		it('leaves the static terms and account-deletion pages to the network too', () => {
			const urls = [...sw.matchAll(/\{url:"([^"]+)"/g)].map((m) => m[1]);
			expect(urls.filter((u) => /^(?:terms|delete-account)/.test(u))).toEqual([]);
			expect(sw).toMatch(
				/denylist:\[[^\]]*\/\^\\\/\(\?:terms\|delete-account\)\(\?:\\\/\|\\\.html\)\?\$\//
			);
		});
	});
});

describe('Digital Asset Links (Android Trusted Web Activity)', () => {
	const file = 'static/.well-known/assetlinks.json';
	const PACKAGE = 'xyz.aresius.tuttifrutti';
	const FINGERPRINT = /^([0-9A-F]{2}:){31}[0-9A-F]{2}$/;
	interface Statement {
		relation: string[];
		target: {
			namespace: string;
			package_name: string;
			sha256_cert_fingerprints: string[];
		};
	}
	const statements = JSON.parse(read(file)) as Statement[];

	it('is valid JSON: an array', () => {
		expect(Array.isArray(statements)).toBe(true);
	});

	it('every statement hands every URL of the site to our app and only to it', () => {
		for (const statement of statements) {
			expect(statement.relation).toEqual(['delegate_permission/common.handle_all_urls']);
			expect(statement.target.namespace).toBe('android_app');
			expect(statement.target.package_name).toBe(PACKAGE);
			expect(statement.target.sha256_cert_fingerprints.length).toBeGreaterThan(0);
			for (const fingerprint of statement.target.sha256_cert_fingerprints) {
				expect(fingerprint).toMatch(FINGERPRINT);
			}
		}
	});

	// The real statement needs the SHA-256 of the Play *app-signing* key, which exists only after
	// the app is created in the Play Console. Until then the file is the empty array, on purpose.
	// Once it is filled in, the PENDING test is skipped and the PUBLISHED one runs instead.
	it.runIf(statements.length === 0)(
		'PENDING: still the empty placeholder, the Play app-signing fingerprint is not known yet',
		() => {
			expect(read(file).trim()).toBe('[]');
		}
	);

	it.runIf(statements.length > 0)('PUBLISHED: one statement for the app', () => {
		expect(statements).toHaveLength(1);
	});

	// Every copy installed from Google Play is signed with Google's app-signing key (Play Console ›
	// App integrity › App signing). Removing it puts every installed app back behind a browser bar.
	it('keeps the Play app-signing key fingerprint', () => {
		expect(statements[0]?.target.sha256_cert_fingerprints).toContain(
			'31:D2:04:E7:7D:40:EE:C7:0C:25:F3:2E:3F:1E:E5:87:A6:10:D1:2E:9F:5E:19:EF:CF:B7:EA:08:AA:2E:35:C0'
		);
	});

	it.runIf(existsSync(join(root, 'build', 'index.html')))('is copied into build/ as it is', () => {
		expect(read('build/.well-known/assetlinks.json')).toBe(read(file));
	});
});
