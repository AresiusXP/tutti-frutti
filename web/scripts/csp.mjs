// Derives the per-build parts of the Content-Security-Policy from the build output (see
// "Security headers and PWA (Phase 5)" in docs/DESIGN.md).
//
// The policy itself lives in nginx-security-headers.conf. Only what changes on every build is
// generated here: the sha256 of the one inline bootstrap <script> that SvelteKit puts in the SPA
// shell (its text contains content-hashed chunk names) and of the static `style="…"` attributes
// the app renders (the shell's wrapper div, SvelteKit's route announcer). The build FAILS when
// index.html contains any other inline code (script, handler, javascript: URL, <style>) or its own
// CSP <meta>, or when a template of the client bundle carries a style attribute that is not in
// EXPECTED_STYLE_ATTRS: such code would be blocked in production, silently, by the policy.
// (Styles set from JS through the CSSOM, i.e. Svelte's `style:` directive and `el.style.x = …`,
// are not restricted by CSP and are not scanned.)
//
// Used by svelte.config.js (runs after adapter-static on every `npm run build`) and by
// `node scripts/csp.mjs [build-dir] [out.conf]`.
import { createHash } from 'node:crypto';
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

/** Static inline `style` attributes the app may render; each one gets a hash in `style-src-attr`. */
export const EXPECTED_STYLE_ATTRS = [
	// the wrapper div of src/app.html
	'display: contents',
	// SvelteKit's route announcer (@sveltejs/kit runtime root.svelte), mounted at start
	'position: absolute; left: 0; top: 0; clip: rect(0 0 0 0); clip-path: inset(50%); overflow: hidden; white-space: nowrap; width: 1px; height: 1px'
];

/**
 * CSP source expression for an inline script/style text (browsers hash the LF-normalised text).
 * @param {string} text
 * @returns {string}
 */
export function cspHash(text) {
	const normalised = text.replaceAll('\r\n', '\n').replaceAll('\r', '\n');
	return `'sha256-${createHash('sha256').update(normalised, 'utf8').digest('base64')}'`;
}

const DATA_BLOCK = /^(?:application\/json|application\/ld\+json)$/i;

/**
 * Inline code of an HTML document: the hash of the SvelteKit bootstrap script, its static style
 * attributes, and everything the CSP would block.
 * @param {string} html
 * @returns {{ scriptHashes: string[], styleAttrs: string[], problems: string[] }}
 */
export function analyzeHtml(html) {
	/** @type {string[]} */
	const problems = [];
	const source = html.replace(/<!--[\s\S]*?-->/g, '');

	/** @type {string[]} */
	const scriptHashes = [];
	const markup = source.replace(
		/<script\b([^>]*)>([\s\S]*?)<\/script\s*>/gi,
		(_all, attrs, body) => {
			if (/(?:^|\s)src\s*=/i.test(attrs)) return '';
			const type = /(?:^|\s)type\s*=\s*["']?([^"'\s>]+)/i.exec(attrs)?.[1] ?? '';
			if (DATA_BLOCK.test(type)) return '';
			if (body.includes('__sveltekit_') && body.includes('kit.start(')) {
				scriptHashes.push(cspHash(body));
			} else {
				problems.push(`unexpected inline <script>: ${JSON.stringify(body.trim().slice(0, 60))}`);
			}
			return '';
		}
	);
	if (scriptHashes.length !== 1) {
		problems.push(`expected exactly one SvelteKit bootstrap script, found ${scriptHashes.length}`);
	}

	if (/<style\b/i.test(markup)) problems.push('unexpected inline <style> element');
	if (/http-equiv\s*=\s*["']?content-security-policy/i.test(markup)) {
		problems.push('index.html carries its own CSP <meta>: the nginx header is the only policy');
	}

	/** @type {string[]} */
	const styleAttrs = [];
	for (const [tag] of markup.matchAll(/<[a-z][^>]*>/gi)) {
		if (/\son[a-z]+\s*=/i.test(tag)) problems.push(`inline event handler in ${tag.slice(0, 60)}`);
		if (/\s(?:href|src|action|formaction)\s*=\s*["']?\s*javascript:/i.test(tag)) {
			problems.push(`javascript: URL in ${tag.slice(0, 60)}`);
		}
		for (const m of tag.matchAll(/\sstyle\s*=\s*(?:"([^"]*)"|'([^']*)')/gi)) {
			styleAttrs.push(m[1] ?? m[2]);
		}
	}
	return { scriptHashes, styleAttrs, problems };
}

/**
 * Static `style="…"` attributes inside the HTML template strings of a compiled JS file.
 * @param {string} js
 * @returns {string[]}
 */
export function findStyleAttrs(js) {
	/** @type {string[]} */
	const found = [];
	for (const m of js.matchAll(/\bstyle=\\?(?:"([^"\\]*)\\?"|'([^'\\]*)\\?')/g)) {
		found.push(m[1] ?? m[2]);
	}
	return found;
}

/**
 * @param {string} dir
 * @returns {Generator<string>}
 */
function* walk(dir) {
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) yield* walk(path);
		else if (entry.name.endsWith('.js')) yield path;
	}
}

/**
 * Validates a build directory and returns the hashes the CSP needs.
 * Throws when the app contains inline code the policy would block.
 * @param {string} buildDir adapter-static output (contains index.html and _app/immutable)
 * @returns {{ scriptHashes: string[], styleAttrHashes: string[] }}
 */
export function analyzeBuild(buildDir) {
	const { scriptHashes, styleAttrs, problems } = analyzeHtml(
		readFileSync(join(buildDir, 'index.html'), 'utf8')
	);
	const where = new Map(styleAttrs.map((value) => [value, 'index.html']));
	let clientDir;
	try {
		clientDir = join(buildDir, '_app', 'immutable');
		readdirSync(clientDir);
	} catch {
		problems.push(`${clientDir} not found: nothing to scan for static style attributes`);
		clientDir = undefined;
	}
	for (const file of clientDir ? walk(clientDir) : []) {
		for (const value of findStyleAttrs(readFileSync(file, 'utf8'))) {
			if (!where.has(value)) where.set(value, file.slice(buildDir.length + 1));
		}
	}

	/** @type {string[]} */
	const styleAttrHashes = [];
	for (const [value, file] of where) {
		if (value.includes('&')) {
			problems.push(`style attribute with HTML entities is not supported (${file}): ${value}`);
		} else if (EXPECTED_STYLE_ATTRS.includes(value)) {
			styleAttrHashes.push(cspHash(value));
		} else {
			problems.push(
				`unexpected static style attribute in ${file}: ${JSON.stringify(value)} (use Svelte's style: directive, which is not blocked, or add it to EXPECTED_STYLE_ATTRS)`
			);
		}
	}

	if (problems.length > 0) {
		throw new Error(
			`The build contains inline code the Content-Security-Policy would block:\n  - ${problems.join('\n  - ')}\n` +
				'Move it to an external file, or extend the policy deliberately in scripts/csp.mjs and nginx-security-headers.conf.'
		);
	}
	return { scriptHashes, styleAttrHashes };
}

/**
 * nginx `map`s (http context) that nginx-security-headers.conf interpolates into the policy.
 * @param {{ scriptHashes: string[], styleAttrHashes: string[] }} hashes
 * @returns {string}
 */
export function renderNginx({ scriptHashes, styleAttrHashes }) {
	/** @type {(name: string, values: string[]) => string} */
	const constant = (name, values) =>
		`map $server_name ${name} {\n\tdefault "${values.join(' ')}";\n}\n`;
	return (
		'# Generated by scripts/csp.mjs from the build output on every build. Do not edit.\n' +
		constant('$tf_script_hashes', scriptHashes) +
		constant('$tf_style_attr_hashes', styleAttrHashes)
	);
}

/**
 * Validates the build and writes the nginx include.
 * @param {{ buildDir: string, outPath: string }} paths
 */
export function writeCspHashes({ buildDir, outPath }) {
	const analysis = analyzeBuild(buildDir);
	mkdirSync(dirname(outPath), { recursive: true });
	writeFileSync(outPath, renderNginx(analysis));
	return analysis;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const [buildDir = 'build', outPath = '.svelte-kit/nginx/csp-hashes.conf'] = process.argv.slice(2);
	const { scriptHashes, styleAttrHashes } = writeCspHashes({ buildDir, outPath });
	console.log(
		`${outPath}: ${scriptHashes.length} script hash(es), ${styleAttrHashes.length} style attribute hash(es)`
	);
}
