#!/usr/bin/env node
// Renders the Google Play feature graphic (1024 x 500 PNG, no alpha channel, as the Play Console
// requires) from the website's artwork. The PNG is committed; run this only when the artwork changes:
//
//   cd web && npm ci && cd ../android && node scripts/generate-feature-graphic.mjs
//
// It uses `sharp` from web/node_modules (web/package.json is not touched). The text is the constant
// game name "Tutti Frutti" (never a translation key) and nothing else: no store-ranking claims, no
// "free", no download call to action, no language-specific words, so the one image serves es, en and nl.
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const out = path.join(root, 'android', 'store', 'feature-graphic-1024x500.png');

let sharp;
try {
	sharp = createRequire(path.join(root, 'web', 'package.json'))('sharp');
} catch {
	console.error('Cannot load "sharp": run `npm ci` in web/ first (it is not an android/ dependency).');
	process.exit(1);
}

const WIDTH = 1024;
const HEIGHT = 500;
const ART_WIDTH = 440; // the logo mark, viewBox 424 x 344
const ART_HEIGHT = Math.round((ART_WIDTH * 344) / 424);

const mark = await readFile(path.join(root, 'web', 'static', 'logo-mark.svg'));
const art = await sharp(mark, { density: 300 }).resize({ width: ART_WIDTH }).png().toBuffer();

const background = Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#2e1a5c" />
      <stop offset="1" stop-color="#1a1033" />
    </linearGradient>
  </defs>
  <rect width="${WIDTH}" height="${HEIGHT}" fill="url(#bg)" />
  <text x="${WIDTH - 70}" y="${HEIGHT / 2 - 6}" text-anchor="end"
        font-family="Arial Black, Arial, Helvetica, sans-serif" font-weight="900" font-size="108" fill="#fff8e7">Tutti</text>
  <text x="${WIDTH - 70}" y="${HEIGHT / 2 + 108}" text-anchor="end"
        font-family="Arial Black, Arial, Helvetica, sans-serif" font-weight="900" font-size="108" fill="#ffc15e">Frutti</text>
</svg>`);

await mkdir(path.dirname(out), { recursive: true });
const png = await sharp(background)
	.composite([{ input: art, left: 60, top: Math.round((HEIGHT - ART_HEIGHT) / 2) }])
	.flatten({ background: '#1a1033' }) // no alpha channel
	.removeAlpha()
	.png({ compressionLevel: 9, palette: false })
	.toBuffer();
await writeFile(out, png);
const meta = await sharp(png).metadata();
console.log(`${path.relative(root, out)}: ${meta.width}x${meta.height}, channels=${meta.channels}, hasAlpha=${meta.hasAlpha}`);
