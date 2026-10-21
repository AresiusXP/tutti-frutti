// Renders static/og-image.png, the link-preview image (og:image) of src/app.html, from the logo
// artwork and the wordmark styling of the app: no server needed.
//
//   npm run og                          (needs a Chromium and playwright-core, which are NOT
//                                        dependencies of this repo)
//
// Environment: CHROME (path of the Chromium binary), PLAYWRIGHT_CORE (module specifier or path to
// import, default "playwright-core").
// Output: a 1200x630 palette PNG, well under MAX_BYTES. Commit the result.
//
// Messengers crop the card differently (Discord/Facebook show 1.91:1, WhatsApp and Telegram may
// crop to a square), so everything that matters sits inside the central 630x630 square, and there
// is no small text: the card is read as a thumbnail.
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';
import { BRAND } from '../src/lib/brand.ts';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const out = resolve(root, 'static', 'og-image.png');
const WIDTH = 1200;
const HEIGHT = 630;
const MAX_BYTES = 150 * 1024;

const mark = (await readFile(resolve(root, 'static', 'logo-mark.svg'), 'utf8')).trim();
const markUri = `data:image/svg+xml;base64,${Buffer.from(mark).toString('base64')}`;

// Same palette as --color-* in src/app.css, same word colours/tilt as Wordmark.svelte.
const colors = ['#ffd23f', '#ff4f81', '#7ddc3b', '#ff8a1f'];
const words = BRAND.split(' ')
	.map(
		(word, i) =>
			`<span style="color:${colors[i % colors.length]};transform:rotate(${i % 2 === 0 ? -3 : 2}deg)">${word}</span>`
	)
	.join('');

const html = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<style>
	html, body { margin: 0; width: ${WIDTH}px; height: ${HEIGHT}px; overflow: hidden; }
	body {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 34px;
		font-family: ui-rounded, 'SF Pro Rounded', 'Nunito', 'Segoe UI', system-ui, -apple-system, sans-serif;
		background:
			radial-gradient(900px 520px at 88% -12%, rgb(255 79 129 / 0.34), transparent 60%),
			radial-gradient(800px 480px at -8% 8%, rgb(255 210 63 / 0.26), transparent 60%),
			radial-gradient(900px 420px at 50% 125%, rgb(155 108 255 / 0.28), transparent 60%),
			#1a1033;
	}
	img { width: 400px; height: auto; filter: drop-shadow(0 8px 0 rgb(0 0 0 / 0.3)); }
	h1 {
		margin: 0;
		display: flex;
		gap: 30px;
		font-size: 112px;
		font-weight: 900;
		line-height: 1;
		letter-spacing: -0.02em;
		white-space: nowrap;
	}
	h1 span { display: inline-block; text-shadow: 0 7px 0 rgb(0 0 0 / 0.35); }
</style>
</head>
<body>
	<img src="${markUri}" alt="" />
	<h1>${words}</h1>
</body>
</html>`;

const { chromium } = await import(process.env.PLAYWRIGHT_CORE ?? 'playwright-core');
const browser = await chromium.launch({
	executablePath: process.env.CHROME || undefined,
	args: ['--no-sandbox']
});
try {
	const page = await browser.newPage({ viewport: { width: WIDTH, height: HEIGHT } });
	await page.setContent(html, { waitUntil: 'load' });
	await page.evaluate(() => document.fonts.ready);
	const raw = await page.screenshot({ type: 'png' });
	const png = await sharp(raw).png({ palette: true, quality: 90, effort: 10 }).toBuffer();
	if (png.length > MAX_BYTES)
		throw new Error(`og-image.png is ${png.length} bytes (max ${MAX_BYTES})`);
	const { width, height } = await sharp(png).metadata();
	if (width !== WIDTH || height !== HEIGHT) throw new Error(`og-image.png is ${width}x${height}`);
	await writeFile(out, png);
	console.log(`static/og-image.png  ${(png.length / 1024).toFixed(0)} KB  (${width}x${height})`);
} finally {
	await browser.close();
}
