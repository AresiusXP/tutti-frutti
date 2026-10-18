// Renders the PNG icons and favicon.ico from static/logo.svg.
// Run with `npm run icons` and commit the results in static/.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const staticDir = resolve(root, 'static');
const iconsDir = resolve(staticDir, 'icons');

const source = await readFile(resolve(staticDir, 'logo.svg'), 'utf8');

// Maskable icons are cropped by the OS to a circle/squircle: full-bleed
// background and artwork scaled into the central safe zone.
const maskableSource = source
	.replace('rx="112"', 'rx="0"')
	.replace(
		'<g id="art">',
		'<g id="art" transform="translate(256 256) scale(0.84) translate(-256 -256)">'
	);
if (maskableSource === source) throw new Error('logo.svg markers not found');

async function png(svg, size) {
	return sharp(Buffer.from(svg), { density: ((72 * size) / 512) * 2 })
		.resize(size, size)
		.png({ compressionLevel: 9 })
		.toBuffer();
}

// An .ico file may embed PNG images directly.
function ico(images) {
	const header = Buffer.alloc(6 + 16 * images.length);
	header.writeUInt16LE(0, 0);
	header.writeUInt16LE(1, 2);
	header.writeUInt16LE(images.length, 4);
	let offset = header.length;
	images.forEach(({ size, data }, i) => {
		const at = 6 + 16 * i;
		header.writeUInt8(size, at);
		header.writeUInt8(size, at + 1);
		header.writeUInt8(0, at + 2);
		header.writeUInt8(0, at + 3);
		header.writeUInt16LE(1, at + 4);
		header.writeUInt16LE(32, at + 6);
		header.writeUInt32LE(data.length, at + 8);
		header.writeUInt32LE(offset, at + 12);
		offset += data.length;
	});
	return Buffer.concat([header, ...images.map((i) => i.data)]);
}

// Tile-less artwork for in-page use (cropped to the fruit). It is decorative
// (<img alt="">), so it drops role/aria-label: that also keeps the brand name
// out of this file (see src/lib/brand.test.ts).
const markSource = source
	.replace(/<rect id="bg"[^>]*\/>\n?\s*/, '')
	.replace('viewBox="0 0 512 512"', 'viewBox="44 70 424 344"')
	.replace(/ role="img" aria-label="[^"]*"/, '');
if (markSource === source) throw new Error('logo.svg markers not found');
await writeFile(resolve(staticDir, 'logo-mark.svg'), markSource);
console.log('wrote static/logo-mark.svg');

await mkdir(iconsDir, { recursive: true });

const outputs = [
	[resolve(iconsDir, 'icon-192.png'), await png(source, 192)],
	[resolve(iconsDir, 'icon-512.png'), await png(source, 512)],
	[resolve(iconsDir, 'maskable-512.png'), await png(maskableSource, 512)],
	[resolve(staticDir, 'apple-touch-icon.png'), await png(maskableSource, 180)],
	[
		resolve(staticDir, 'favicon.ico'),
		ico(
			await Promise.all([16, 32, 48].map(async (size) => ({ size, data: await png(source, size) })))
		)
	]
];

for (const [file, data] of outputs) {
	await writeFile(file, data);
	console.log(`wrote ${file.replace(root + '/', '')} (${data.length} bytes)`);
}
