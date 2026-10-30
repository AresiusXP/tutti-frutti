#!/usr/bin/env node
// Regenerates the launcher icons and the splash image of the Android shell from the website's
// artwork, at the sizes Bubblewrap 1.27.0 uses (TwaGenerator: IMAGES / ADAPTIVE_IMAGES /
// SPLASH_IMAGES). The PNGs are committed; run this only when the artwork changes:
//
//   cd web && npm ci && cd ../android && node scripts/generate-icons.mjs
//
// It uses `sharp` from web/node_modules (web/package.json is not touched).
//
//   mipmap-*/ic_launcher.png  <- web/static/icons/icon-512.png        (48/72/96/144/192)
//   mipmap-*/ic_maskable.png  <- web/static/icons/maskable-512.png    (82/123/164/246/328)
//   drawable-*/splash.png     <- web/static/logo-mark.svg, centred on a transparent square
//                                (300/450/600/900/1200); the splash screen paints the
//                                #1a1033 background colour behind it.
import { createRequire } from 'node:module';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const res = path.join(root, 'android', 'app', 'src', 'main', 'res');

let sharp;
try {
	sharp = createRequire(path.join(root, 'web', 'package.json'))('sharp');
} catch {
	console.error('Cannot load "sharp": run `npm ci` in web/ first (it is not an android/ dependency).');
	process.exit(1);
}

const DENSITIES = ['mdpi', 'hdpi', 'xhdpi', 'xxhdpi', 'xxxhdpi'];
const LAUNCHER = [48, 72, 96, 144, 192];
const MASKABLE = [82, 123, 164, 246, 328];
const SPLASH = [300, 450, 600, 900, 1200];
// The logo occupies this share of the splash canvas width (viewBox 424x344 -> aspect ratio).
const SPLASH_ART_SHARE = 0.6;
const SVG_WIDTH = 424;

// `palette: false` matters: sharp's `effort` option would quantise the gradients to 256 colours.
const PNG = { compressionLevel: 9, palette: false };

async function resizeRaster(source, size, dest) {
	await mkdir(path.dirname(dest), { recursive: true });
	await sharp(source)
		.resize(size, size, { fit: 'fill', kernel: 'lanczos3' })
		.png(PNG)
		.toFile(dest);
	console.log(`${path.relative(root, dest)} (${size}x${size})`);
}

async function renderSplash(svg, size, dest) {
	const artWidth = Math.round(size * SPLASH_ART_SHARE);
	// Rasterise the vector at 2x the target width, then downscale: crisp edges at every size.
	const density = Math.ceil((72 * artWidth * 2) / SVG_WIDTH);
	const art = await sharp(svg, { density })
		.resize({ width: artWidth, kernel: 'lanczos3' })
		.png()
		.toBuffer();
	await mkdir(path.dirname(dest), { recursive: true });
	await sharp({
		create: { width: size, height: size, channels: 4, background: { r: 0, g: 0, b: 0, alpha: 0 } }
	})
		.composite([{ input: art, gravity: 'centre' }])
		.png(PNG)
		.toFile(dest);
	console.log(`${path.relative(root, dest)} (${size}x${size})`);
}

const anyIcon = path.join(root, 'web', 'static', 'icons', 'icon-512.png');
const maskableIcon = path.join(root, 'web', 'static', 'icons', 'maskable-512.png');
const logoMark = await readFile(path.join(root, 'web', 'static', 'logo-mark.svg'));

for (const [i, density] of DENSITIES.entries()) {
	await resizeRaster(anyIcon, LAUNCHER[i], path.join(res, `mipmap-${density}`, 'ic_launcher.png'));
	await resizeRaster(maskableIcon, MASKABLE[i], path.join(res, `mipmap-${density}`, 'ic_maskable.png'));
	await renderSplash(logoMark, SPLASH[i], path.join(res, `drawable-${density}`, 'splash.png'));
}
