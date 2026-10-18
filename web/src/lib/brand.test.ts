import { readdirSync, readFileSync } from 'node:fs';
import { relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BRAND } from './brand';

const root = fileURLToPath(new URL('../../', import.meta.url));

/**
 * The only files under src/ and static/ that may contain the literal brand
 * (any casing). Everything else must use `BRAND` from src/lib/brand.ts, or a
 * `{brand}` message parameter. Add a file here only with a justification.
 */
const ALLOWED_LITERALS: Record<string, string> = {
	'src/app.html':
		'static HTML shell (<title>, application/og meta) is served before any JS runs and cannot import BRAND',
	'static/logo.svg': 'logo source artwork (aria-label); the PNG icons are rendered from it',
	'static/privacy.html':
		'static legal page (no script, so it cannot import BRAND); a test checks it is written exactly "Tutti Frutti"'
};

const SCANNED_DIRS = ['src', 'static'];
const EXCLUDED = [
	/^src\/lib\/brand\.ts$/,
	/^src\/lib\/paraglide\//,
	/\.(?:test|spec)\.[cm]?[jt]sx?$/
];
const BRAND_PATTERN = /tutti\s+frutti/gi;

function textFiles(dir: string): string[] {
	return readdirSync(`${root}${dir}`, { recursive: true, withFileTypes: true })
		.filter((entry) => entry.isFile())
		.map((entry) => relative(root, `${entry.parentPath}/${entry.name}`).split('\\').join('/'))
		.filter((file) => !EXCLUDED.some((pattern) => pattern.test(file)));
}

function isBinary(buffer: Buffer): boolean {
	return buffer.subarray(0, 8000).includes(0);
}

function scan() {
	const hits = new Map<string, string[]>();
	for (const file of SCANNED_DIRS.flatMap(textFiles)) {
		const buffer = readFileSync(`${root}${file}`);
		if (isBinary(buffer)) continue;
		const matches = buffer.toString('utf8').match(BRAND_PATTERN);
		if (matches) hits.set(file, matches);
	}
	return hits;
}

describe('brand literal', () => {
	const hits = scan();

	it('scans a meaningful set of files', () => {
		expect(textFiles('src').length).toBeGreaterThan(10);
		expect(textFiles('static').length).toBeGreaterThan(2);
	});

	it('only appears in the explicit allowlist (new duplicates fail here)', () => {
		expect([...hits.keys()].sort()).toEqual(Object.keys(ALLOWED_LITERALS).sort());
	});

	it.each(Object.keys(ALLOWED_LITERALS))('every occurrence in %s equals BRAND exactly', (file) => {
		const found = hits.get(file) ?? [];
		expect(found.length).toBeGreaterThan(0);
		for (const value of found) expect(value).toBe(BRAND);
	});
});
