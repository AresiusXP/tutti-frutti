import { defineConfig, devices } from '@playwright/test';
import { BASE_URL } from './src/env';

/**
 * Google Play store screenshots (not a test suite): `store/screenshots.spec.ts` plays one short game per
 * language against the compose stack and saves seven phone screenshots of the real app per language in
 * `android/store/screenshots/<lang>/`.
 *
 *   cd e2e && npm run store:screenshots      # builds and starts the stack, tears it down afterwards
 *
 * 405x720 CSS pixels at 8/3 = 1080x1920 (9:16), the size Play recommends for phone screenshots.
 */
export default defineConfig({
	testDir: './store',
	outputDir: './test-results/store',
	timeout: 300_000,
	expect: { timeout: 15_000 },
	workers: 1,
	fullyParallel: false,
	retries: 0,
	reporter: [['list']],
	use: {
		baseURL: BASE_URL,
		...devices['Pixel 7'],
		viewport: { width: 405, height: 720 },
		deviceScaleFactor: 8 / 3,
		actionTimeout: 10_000,
		navigationTimeout: 20_000,
		trace: 'retain-on-failure',
		video: 'off',
		screenshot: 'off',
		serviceWorkers: 'allow'
	}
});
