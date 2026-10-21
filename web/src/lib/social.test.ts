import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';

const root = fileURLToPath(new URL('../../', import.meta.url));
const read = (file: string) => readFileSync(join(root, file), 'utf8');

/** The static shell is all a link-preview crawler (Discord, WhatsApp, ...) ever sees: it runs no JS. */
const html = read('src/app.html').replace(/<!--[\s\S]*?-->/g, '');

function tag(attr: 'property' | 'name', key: string): string | undefined {
	for (const [element] of html.matchAll(/<meta\b[^>]*>/g)) {
		const own = new RegExp(`\\s${attr}="${key}"`).test(element);
		if (own) return /\scontent="([^"]*)"/.exec(element)?.[1];
	}
	return undefined;
}

const ORIGIN = 'https://tuttifrutti.aresius.xyz';

describe('link previews (Open Graph) in the static shell', () => {
	it('names the game exactly, with a description of a sensible length', () => {
		expect(tag('property', 'og:title')).toBe(BRAND);
		expect(tag('property', 'og:site_name')).toBe(BRAND);
		expect(tag('property', 'og:type')).toBe('website');
		const description = tag('property', 'og:description') ?? '';
		expect(description.length).toBeGreaterThan(40);
		// chat apps cut longer descriptions
		expect(description.length).toBeLessThanOrEqual(200);
		expect(tag('property', 'og:locale')).toBe('es_AR');
	});

	it('points og:image at an absolute https URL of a real 1200x630 PNG', () => {
		const url = tag('property', 'og:image') ?? '';
		expect(url.startsWith(`${ORIGIN}/`), url).toBe(true);
		const file = `static${url.slice(ORIGIN.length)}`;
		expect(file).toBe('static/og-image.png');

		const data = readFileSync(join(root, file));
		expect(data.subarray(1, 4).toString('latin1'), 'is a PNG').toBe('PNG');
		const size = `${data.readUInt32BE(16)}x${data.readUInt32BE(20)}`;
		expect(size).toBe('1200x630');
		expect(tag('property', 'og:image:width')).toBe('1200');
		expect(tag('property', 'og:image:height')).toBe('630');
		expect(tag('property', 'og:image:type')).toBe('image/png');
		// some crawlers give up on big images (WhatsApp: ~300 KB)
		expect(statSync(join(root, file)).size).toBeLessThanOrEqual(300 * 1024);
	});

	it('has alt text for the image and asks for the large card', () => {
		expect(tag('property', 'og:image:alt')?.length).toBeGreaterThan(10);
		expect(tag('name', 'twitter:card')).toBe('summary_large_image');
	});

	it('has no og:url: every invite link (/KT7P) shares this shell and must not canonicalise to Home', () => {
		expect(tag('property', 'og:url')).toBeUndefined();
	});

	it('names the game in the image alt text', () => {
		expect(tag('property', 'og:image:alt')).toContain(BRAND);
	});

	it('calls the game by its name: "Basta" is what a player calls to end a round, not the game', () => {
		const description = tag('property', 'og:description') ?? '';
		expect(description).toContain(BRAND);
		expect(description).not.toMatch(/\bbasta\b/i);
	});

	it('never precaches the preview image: only crawlers fetch it', () => {
		expect(read('vite.config.ts')).toMatch(/globIgnores:\s*\[[^\]]*'\*\*\/og-image\.png'/);
	});
});
