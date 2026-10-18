// Renders static/screenshots/*.png (the `screenshots` of the web app manifest) from the dev
// playground: deterministic FakeSession scenarios, no server needed.
//
//   npm run dev -- --port 5173          (in another terminal)
//   node scripts/screenshots.mjs        (needs a Chromium and playwright-core, which are NOT
//                                        dependencies of this repo)
//
// Environment: DEV_URL (default http://localhost:5173), CHROME (path of the Chromium binary),
// PLAYWRIGHT_CORE (module specifier or path to import, default "playwright-core").
// Output: 540x960 palette PNGs (a 360x640 viewport at 1.5x), each well under 150 KB.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const outDir = resolve(root, 'static', 'screenshots');
const devUrl = process.env.DEV_URL ?? 'http://localhost:5173';
const MAX_BYTES = 150 * 1024;

const { chromium } = await import(process.env.PLAYWRIGHT_CORE ?? 'playwright-core');
const list = JSON.parse(await readFile(resolve(root, 'scripts', 'screenshots.json'), 'utf8'));

await mkdir(outDir, { recursive: true });
const browser = await chromium.launch({
	executablePath: process.env.CHROME || undefined,
	args: ['--no-sandbox']
});
const context = await browser.newContext({
	viewport: { width: 360, height: 640 },
	deviceScaleFactor: 1.5,
	locale: 'es-AR',
	reducedMotion: 'reduce'
});
const page = await context.newPage();
try {
	for (const { name, scenario } of list) {
		await page.goto(`${devUrl}/dev?s=${scenario}&lang=es&bare=1`, { waitUntil: 'networkidle' });
		await page.waitForTimeout(1500);
		const raw = await page.screenshot();
		const png = await sharp(raw).png({ palette: true, quality: 90, effort: 10 }).toBuffer();
		if (png.length > MAX_BYTES)
			throw new Error(`${name}.png is ${png.length} bytes (max ${MAX_BYTES})`);
		await writeFile(resolve(outDir, `${name}.png`), png);
		console.log(
			`static/screenshots/${name}.png  ${(png.length / 1024).toFixed(0)} KB  (${scenario})`
		);
	}
} finally {
	await browser.close();
}
