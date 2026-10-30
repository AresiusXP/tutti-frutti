import { defineConfig, devices } from '@playwright/test';
import { BASE_URL, WORKERS } from './src/env';

/**
 * Projects:
 *  - `chromium`: Pixel 7 emulation (390x844), every spec except the two below; the default fast run.
 *  - `slow`: the specs that must wait for the server's own clock (the 60 s round time limit).
 *  - `restart`: restarts the server container, which every other test would notice, so it runs alone,
 *    after the two above (project dependencies). Skipped unless E2E_COMPOSE_PROJECT is set.
 *  - `deploy`: replaces the worker script inside the web container (`*-update-deploy.restart.spec.ts`).
 *    It must not overlap the server restart above (the 502s of a restarting server would reach its
 *    browsers), and files of one project run in parallel workers, so it is a project of its own that
 *    runs after `restart`. Another spec that changes a container gets its own project in the same chain.
 * `npx playwright test` runs all; `--project=chromium` is the quick loop.
 */
const phone = {
	...devices['Pixel 7'],
	viewport: { width: 390, height: 844 }
};

export default defineConfig({
	testDir: './tests',
	outputDir: './test-results',
	timeout: 150_000,
	expect: { timeout: 10_000 },
	// Rooms are independent, but the server rate limits are shared global buckets (see README).
	workers: WORKERS,
	fullyParallel: false,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	reporter: [
		['list'],
		['html', { open: 'never', outputFolder: 'playwright-report' }],
		...(process.env.CI ? ([['github']] as const) : [])
	],
	use: {
		baseURL: BASE_URL,
		...phone,
		actionTimeout: 10_000,
		navigationTimeout: 20_000,
		trace: 'retain-on-failure',
		video: 'off',
		screenshot: 'off',
		serviceWorkers: 'allow'
	},
	projects: [
		{
			name: 'chromium',
			testIgnore: /\.(slow|restart)\.spec\.ts$/
		},
		{
			name: 'slow',
			testMatch: /\.slow\.spec\.ts$/,
			timeout: 240_000
		},
		{
			name: 'restart',
			testMatch: /\.restart\.spec\.ts$/,
			testIgnore: /-update-deploy\.restart\.spec\.ts$/,
			dependencies: ['chromium', 'slow'],
			timeout: 180_000
		},
		{
			name: 'deploy',
			testMatch: /-update-deploy\.restart\.spec\.ts$/,
			dependencies: ['restart'],
			timeout: 180_000
		}
	]
});
