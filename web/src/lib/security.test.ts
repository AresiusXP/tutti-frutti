import {
	existsSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	rmSync,
	writeFileSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import {
	analyzeBuild,
	analyzeHtml,
	cspHash,
	EXPECTED_STYLE_ATTRS,
	findStyleAttrs,
	renderNginx,
	writeCspHashes
} from '../../scripts/csp.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
const read = (file: string) => readFileSync(join(root, file), 'utf8');

/** What SvelteKit's static adapter puts in the SPA shell (shape, not the real hashes). */
const BOOTSTRAP = `
				{
					__sveltekit_abc123 = {
						base: ""
					};

					const element = document.currentScript.parentElement;

					Promise.all([import("/_app/immutable/entry/start.X.js")]).then(([kit, app]) => {
						kit.start(app, element);
					});
				}
			`;
const shell = (extraHead = '', extraBody = '') => `<!doctype html>
<html lang="es"><head><meta charset="utf-8" />${extraHead}</head>
<body><div style="display: contents">
<script>${BOOTSTRAP}</script>${extraBody}</div></body></html>`;

describe('Content-Security-Policy in nginx-security-headers.conf', () => {
	const conf = read('nginx-security-headers.conf');
	const header = /^add_header Content-Security-Policy "([^"]*)" always;$/m.exec(conf);
	const policy = header?.[1] ?? '';
	const directives = new Map(
		policy
			.replace(/\$tf_csp_upgrade$/, '')
			.split(';')
			.map((part) => part.trim())
			.filter(Boolean)
			.map((part) => {
				const [name, ...sources] = part.split(/\s+/);
				return [name, sources] as const;
			})
	);

	it('is sent on every response as one line', () => {
		expect(header).not.toBeNull();
		expect(policy).not.toMatch(/[\r\n]/);
	});

	it.each([
		['default-src', ["'self'"]],
		['script-src', ["'self'", '$tf_script_hashes']],
		['style-src', ["'self'"]],
		['style-src-attr', ["'unsafe-hashes'", '$tf_style_attr_hashes']],
		['img-src', ["'self'"]],
		['font-src', ["'self'"]],
		['connect-src', ["'self'", '$tf_ws_sources']],
		['manifest-src', ["'self'"]],
		['worker-src', ["'self'"]],
		['object-src', ["'none'"]],
		['base-uri', ["'self'"]],
		['form-action', ["'self'"]],
		['frame-ancestors', ["'none'"]]
	])('%s is exactly %j', (name, sources) => {
		expect(directives.get(name)).toEqual(sources);
	});

	it('never allows inline or eval code, wildcards or arbitrary schemes', () => {
		for (const [name, sources] of directives) {
			for (const source of sources) {
				expect(source, `${name} ${source}`).not.toMatch(
					/^(?:\*|'unsafe-inline'|'unsafe-eval'|'wasm-unsafe-eval'|'strict-dynamic'|https?:|wss?:|data:|blob:|filesystem:)/
				);
			}
		}
		expect(directives.get('script-src')).not.toContain("'unsafe-hashes'");
	});

	it('appends upgrade-insecure-requests only through the https-only variable', () => {
		expect(policy.endsWith('$tf_csp_upgrade')).toBe(true);
		expect(policy).not.toContain('upgrade-insecure-requests');
		const nginx = read('nginx.conf');
		expect(nginx).toMatch(
			/map \$http_x_forwarded_proto \$tf_csp_upgrade \{[^}]*"; upgrade-insecure-requests"/
		);
	});

	it('keeps the other security headers', () => {
		expect(conf).toMatch(/add_header X-Content-Type-Options "nosniff" always;/);
		expect(conf).toMatch(/add_header Referrer-Policy "strict-origin-when-cross-origin" always;/);
		expect(conf).toMatch(/add_header X-Frame-Options "DENY" always;/);
		expect(conf).toMatch(/add_header Cross-Origin-Opener-Policy "same-origin" always;/);
		expect(conf).toMatch(/add_header Cross-Origin-Resource-Policy "same-origin" always;/);
	});

	it('denies unused powerful features but keeps wake lock, share and clipboard writes', () => {
		const permissions = /add_header Permissions-Policy "([^"]*)" always;/.exec(conf)?.[1] ?? '';
		for (const feature of ['camera', 'microphone', 'geolocation', 'payment', 'usb']) {
			expect(permissions).toContain(`${feature}=()`);
		}
		for (const used of ['screen-wake-lock', 'web-share', 'clipboard-write', 'vibrate']) {
			expect(permissions).not.toMatch(new RegExp(`${used}=\\(\\)`));
		}
	});

	it('sends HSTS for https requests only, without includeSubDomains or preload', () => {
		expect(conf).toMatch(/add_header Strict-Transport-Security \$tf_hsts always;/);
		const nginx = read('nginx.conf');
		const map = /map \$http_x_forwarded_proto \$tf_hsts \{([^}]*)\}/.exec(nginx)?.[1] ?? '';
		expect(map).toMatch(/default\s+"";/);
		expect(map).toMatch(/https\s+"max-age=31536000";/);
		expect(map).not.toMatch(/includeSubDomains|preload/i);
	});
});

describe('nginx.conf', () => {
	const nginx = read('nginx.conf').replace(/#.*$/gm, '');
	const SNIPPET = 'include /etc/nginx/snippets/security-headers.conf;';

	/** [name, body] of every `location` block (nested braces such as `types { }` are balanced). */
	function locationBlocks(text: string): (readonly [string, string])[] {
		const blocks: (readonly [string, string])[] = [];
		for (const start of text.matchAll(/\blocation\s+([^{]+?)\s*\{/g)) {
			let depth = 1;
			let end = start.index + start[0].length;
			while (depth > 0 && end < text.length) {
				if (text[end] === '{') depth++;
				if (text[end] === '}') depth--;
				end++;
			}
			blocks.push([start[1], text.slice(start.index + start[0].length, end - 1)]);
		}
		return blocks;
	}
	const locations = locationBlocks(nginx);

	it('finds the locations it is checking', () => {
		expect(locations.length).toBeGreaterThanOrEqual(7);
	});

	it('includes the security headers at server level and in every location that sets a header', () => {
		let serverLevel = nginx;
		for (const [, body] of locations) serverLevel = serverLevel.replace(body, '');
		expect(serverLevel, `outside ${locations.length} locations`).toContain(SNIPPET);
		for (const [name, body] of locations) {
			if (/\badd_header\b/.test(body)) expect(body, `location ${name}`).toContain(SNIPPET);
		}
	});

	it('answers the unknown backend namespace and /healthz with the headers too', () => {
		const get = (needle: string) => locations.find(([name]) => name.includes(needle))?.[1] ?? '';
		expect(get('healthz')).toContain(SNIPPET);
		expect(get('(?:api|ws)')).toContain(SNIPPET);
	});

	it('never caches an error response of the static asset locations', () => {
		for (const needle of ['/_app/immutable/', '(?:png|jpe?g']) {
			const body = locations.find(([name]) => name.includes(needle))?.[1] ?? '';
			expect(body, needle).toMatch(/add_header Cache-Control "public, max-age=\d+[^"]*";/);
			expect(body, needle).not.toMatch(/Cache-Control[^;]*" always;/);
		}
	});

	it('loads the generated hashes before the maps that use them', () => {
		expect(nginx).toContain('include /etc/nginx/snippets/csp-hashes.conf;');
		expect(nginx.indexOf('csp-hashes.conf')).toBeLessThan(nginx.indexOf('server {'));
	});

	describe('$tf_ws_sources host pattern', () => {
		const pattern = /"~\*(\^\(\?<h>[^"]+\$)" "ws:\/\/\$h wss:\/\/\$h";/.exec(nginx)?.[1] ?? '';
		const host = new RegExp(pattern, 'i');

		it.each(['localhost:18300', 'tuttifrutti.aresius.xyz', '127.0.0.1:8000', 'Example.ORG'])(
			'accepts %s',
			(value) => {
				expect(host.test(value)).toBe(true);
			}
		);

		it.each([
			'evil.com; script-src *',
			"x' 'unsafe-inline",
			'a b',
			'a;b',
			"a'b",
			'a,b',
			'a"b',
			'host:',
			'host:123456',
			'[::1]:80',
			'host/path',
			'host\nscript-src *',
			''
		])('rejects %j (nothing can be injected into the policy)', (value) => {
			expect(host.test(value)).toBe(false);
		});
	});
});

describe('inline code detection (scripts/csp.mjs)', () => {
	it('hashes exactly like a browser does (known vector, LF-normalised)', () => {
		expect(cspHash('alert(1)')).toBe("'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF+pI='");
		expect(cspHash('a\r\nb')).toBe(cspHash('a\nb'));
		expect(cspHash('display: contents')).toBe(
			"'sha256-tcbDxjMo+xKqM21aCGYbs/QAJqB7yUXC06oPWDapBgc='"
		);
	});

	it('accepts the SvelteKit shell: one bootstrap script, the wrapper style attribute', () => {
		const result = analyzeHtml(shell());
		expect(result.problems).toEqual([]);
		expect(result.scriptHashes).toEqual([cspHash(BOOTSTRAP)]);
		expect(result.styleAttrs).toEqual(['display: contents']);
	});

	it('ignores external scripts, JSON data blocks and commented-out code', () => {
		const html = shell(
			'<script src="/a.js"></script><script type="application/json">{"a":1}</script><!-- <script>alert(1)</script> -->'
		);
		expect(analyzeHtml(html).problems).toEqual([]);
	});

	it.each([
		[
			'a second inline script',
			shell('', '<script>alert(1)</script>'),
			/unexpected inline <script>/
		],
		[
			'an inline module script',
			shell('<script type="module">import("/x.js")</script>'),
			/unexpected inline <script>/
		],
		[
			'an inline importmap',
			shell('<script type="importmap">{}</script>'),
			/unexpected inline <script>/
		],
		[
			'an inline event handler',
			shell('', '<img src="/x.png" onerror="alert(1)">'),
			/inline event handler/
		],
		['a javascript: URL', shell('', '<a href="javascript:alert(1)">x</a>'), /javascript: URL/],
		['an inline <style>', shell('<style>body{color:red}</style>'), /inline <style>/],
		[
			'a CSP meta tag (header and meta could disagree)',
			shell('<meta http-equiv="content-security-policy" content="default-src *">'),
			/own CSP <meta>/
		],
		[
			'no bootstrap script at all',
			'<!doctype html><html><body><div style="display: contents"></div></body></html>',
			/exactly one SvelteKit bootstrap script, found 0/
		],
		[
			'two bootstrap scripts',
			shell('', `<script>${BOOTSTRAP}</script>`),
			/exactly one SvelteKit bootstrap script, found 2/
		]
	])('flags %s', (_name, html, message) => {
		expect(analyzeHtml(html).problems.join('\n')).toMatch(message);
	});

	it('finds static style attributes in compiled templates but not CSSOM style code', () => {
		expect(findStyleAttrs('y(`<div style="color: red"><!></div>`)')).toEqual(['color: red']);
		expect(findStyleAttrs(`var a="<p style=\\"width: 1px\\">x</p>"`)).toEqual(['width: 1px']);
		expect(findStyleAttrs(`z('<p style=\\'top: 0\\'>')`)).toEqual(['top: 0']);
		expect(
			findStyleAttrs('el.style.setProperty("width","1px");a={style:"long"};e.style.cssText="a:b"')
		).toEqual([]);
	});

	describe('build directory', () => {
		const dirs: string[] = [];
		afterEach(() => {
			for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
		});

		function build(files: Record<string, string>, html = shell()) {
			const dir = mkdtempSync(join(tmpdir(), 'csp-build-'));
			dirs.push(dir);
			const all: Record<string, string> = {
				'index.html': html,
				'_app/immutable/chunks/a.js': '',
				...files
			};
			for (const [name, content] of Object.entries(all)) {
				mkdirSync(dirname(join(dir, name)), { recursive: true });
				writeFileSync(join(dir, name), content);
			}
			return dir;
		}

		const announcer = EXPECTED_STYLE_ATTRS.find((value) => value.startsWith('position: absolute'));

		it('hashes the bootstrap script and every expected style attribute', () => {
			const dir = build({
				'_app/immutable/entry/app.js': `D=y(\`<div id="svelte-announcer" style="${announcer}"></div>\`)`
			});
			expect(analyzeBuild(dir)).toEqual({
				scriptHashes: [cspHash(BOOTSTRAP)],
				styleAttrHashes: [cspHash('display: contents'), cspHash(announcer ?? '')]
			});
		});

		it('fails with the file name when a component renders an unexpected style attribute', () => {
			const dir = build({ '_app/immutable/nodes/3.js': 'y(`<div style="width: 3px">x</div>`)' });
			expect(() => analyzeBuild(dir)).toThrow(/_app\/immutable\/nodes\/3\.js.*"width: 3px"/s);
		});

		it('fails when the shell gained an inline script', () => {
			const dir = build({}, shell('', '<script>fetch("//evil")</script>'));
			expect(() => analyzeBuild(dir)).toThrow(/Content-Security-Policy would block/);
		});

		it('fails when there is no client bundle to scan', () => {
			const dir = build({});
			rmSync(join(dir, '_app'), { recursive: true });
			expect(() => analyzeBuild(dir)).toThrow(/nothing to scan/);
		});

		it('writes the nginx maps the policy interpolates', () => {
			const dir = build({});
			const outPath = join(dir, 'out', 'csp-hashes.conf');
			writeCspHashes({ buildDir: dir, outPath });
			const text = readFileSync(outPath, 'utf8');
			expect(text).toContain(
				`map $server_name $tf_script_hashes {\n\tdefault "${cspHash(BOOTSTRAP)}";`
			);
			expect(text).toContain(
				`map $server_name $tf_style_attr_hashes {\n\tdefault "${cspHash('display: contents')}";`
			);
			expect(renderNginx({ scriptHashes: ['a', 'b'], styleAttrHashes: [] })).toContain(
				'default "a b";'
			);
		});
	});
});

describe('the app sources', () => {
	it('src/app.html has no inline script, handler, <style> or unknown style attribute', () => {
		const html = read('src/app.html');
		expect(html).not.toMatch(/<script\b(?![^>]*\bsrc=)/i);
		expect(html).not.toMatch(/\son[a-z]+\s*=/i);
		expect(html).not.toMatch(/<style\b/i);
		expect(html).not.toMatch(/http-equiv\s*=\s*["']?content-security-policy/i);
		for (const [, value] of html.matchAll(/\sstyle\s*=\s*"([^"]*)"/g)) {
			expect(EXPECTED_STYLE_ATTRS).toContain(value);
		}
	});

	it('Svelte templates carry no fully static style="" attribute', () => {
		const offenders = readdirSync(join(root, 'src'), { recursive: true, withFileTypes: true })
			.filter((entry) => entry.isFile() && entry.name.endsWith('.svelte'))
			.map((entry) => join(entry.parentPath, entry.name))
			.filter((file) => /\sstyle\s*=\s*"[^"{}]*"/.test(readFileSync(file, 'utf8')))
			.map((file) => file.slice(root.length));
		// A static style attribute is part of the template HTML and is blocked by the CSP
		// (style-src-attr): use Svelte's `style:prop={value}` directive, which goes through the CSSOM.
		expect(offenders).toEqual([]);
	});

	describe.runIf(existsSync(join(root, 'build', 'index.html')))('the built output', () => {
		it('contains only inline code the policy knows about', () => {
			expect(() => analyzeBuild(join(root, 'build'))).not.toThrow();
		});

		// absent after a build made before the CSP step existed: `npm run build` creates it
		it.skipIf(!existsSync(join(root, '.svelte-kit', 'nginx', 'csp-hashes.conf')))(
			'matches the generated nginx hashes',
			() => {
				const generated = join(root, '.svelte-kit', 'nginx', 'csp-hashes.conf');
				const { scriptHashes, styleAttrHashes } = analyzeBuild(join(root, 'build'));
				expect(readFileSync(generated, 'utf8')).toBe(
					renderNginx({ scriptHashes, styleAttrHashes })
				);
			}
		);
	});
});
