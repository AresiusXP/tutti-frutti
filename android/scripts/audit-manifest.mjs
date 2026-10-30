#!/usr/bin/env node
// Audits the MERGED Android manifest (what the app really ships: our manifest plus everything the
// libraries add) and fails the build when it drifts from the shell's security posture:
//
//   1. package is xyz.aresius.tuttifrutti
//   2. no <uses-permission> outside the allow-list below (the shell needs none: the browser does
//      the networking; the one entry is added by AndroidX itself, see ALLOWED_PERMISSIONS)
//   3. <application android:allowBackup="false" android:debuggable != "true" (release only)
//   4. every <intent-filter> that matches web URLs (https/http) has a host and path
//      restrictions (path, pathPattern) and none of them is a catch-all (pathPrefix, ".*", ...)
//   5. only the allow-listed components are exported (a library upgrade must not silently add
//      an exported component that can open URLs)
//   6. the components androidbrowserhelper needs at runtime are declared (REQUIRED_COMPONENTS):
//      a missing one crashes the app on launch, which no unit test sees
//   7. optionally the versionName/versionCode match the expected ones
//
// Usage:
//   node scripts/audit-manifest.mjs [MANIFEST] [--variant release]
//                                   [--expect-version-name X.Y.Z] [--expect-version-code N]
//                                   [--allow-permission NAME]... [--allow-debuggable]
//
// Without MANIFEST it reads the single AndroidManifest.xml under
// app/build/intermediates/merged_manifests/<variant>/ (run `./gradlew bundleRelease` first).
// Exit code: 0 ok, 1 audit failed, 2 bad usage. Pure node, no dependencies.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const EXPECTED_PACKAGE = 'xyz.aresius.tuttifrutti';

/**
 * Permissions that may appear in the merged manifest.
 *
 * `<package>.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION` is declared (signature level, so only this
 * app can hold it) and requested by androidx.core since 1.9 to protect dynamically registered
 * receivers. It grants nothing to anyone else. INTERNET is NOT allowed: the Trusted Web Activity
 * and the Custom Tabs fallback run in the browser, the shell itself never opens a socket.
 */
export const ALLOWED_PERMISSIONS = [`${EXPECTED_PACKAGE}.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION`];

/**
 * Components that may be exported (reachable by other apps).
 *  - LauncherActivity: the launcher entry and the verified App Links (validates every URL).
 *  - ProfileInstallReceiver: androidx.profileinstaller, protected by android.permission.DUMP
 *    (only the shell user / system can call it).
 *  - DelegationService: notification delegation; its enabled/exported attributes are the
 *    @bool/enableNotification resource, which build.gradle sets to false.
 */
export const ALLOWED_EXPORTED = [
	`${EXPECTED_PACKAGE}.LauncherActivity`,
	'androidx.profileinstaller.ProfileInstallReceiver',
	`${EXPECTED_PACKAGE}.DelegationService`
];

/**
 * Components the app cannot start without.
 *  - ManageDataLauncherActivity: androidbrowserhelper's LauncherActivity.onCreate always calls
 *    ManageDataLauncherActivity.addSiteSettingsShortcut(), which registers a launcher shortcut for
 *    this component. Android throws "Component class ... does not exist" when it is not declared and
 *    the app closes before anything shows (seen on Android 15). Not exported.
 */
export const REQUIRED_COMPONENTS = ['com.google.androidbrowserhelper.trusted.ManageDataLauncherActivity'];

const COMPONENT_TAGS = new Set(['activity', 'activity-alias', 'service', 'receiver', 'provider']);
const PATH_ATTRIBUTES = ['android:path', 'android:pathPattern', 'android:pathPrefix', 'android:pathAdvancedPattern', 'android:pathSuffix'];
const WEB_SCHEMES = new Set(['http', 'https']);
// What the verified web filter may match: https only, this host only, exactly these pages and
// room invites ("/" + 4 characters). It mirrors LaunchUrlValidator; a change needs both.
export const EXPECTED_WEB_HOST = 'tuttifrutti.aresius.xyz';
export const EXPECTED_WEB_PATHS = ['/', '/rooms', '/stats', '/privacy', '/terms', '/delete-account'];
export const EXPECTED_WEB_PATH_PATTERNS = ['/....'];
// android:pathPattern is a glob: "." is any character and "*" repeats the previous one, so a
// pattern made only of dots and stars with at least one star (".*", "/.*", "/..*") matches
// (nearly) every path. Fixed-length patterns such as "/...." (room invites) are fine.
const isCatchAllPattern = (value) => value === '' || (/^\/?[.*]*$/.test(value) && value.includes('*'));

const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" };
const decode = (text) => text.replace(/&(amp|lt|gt|quot|apos);/g, (_, name) => ENTITIES[name]);

// One token per call, anchored at the "<" (sticky). Attribute values may contain ">" in quotes.
const TOKEN = /<!--[\s\S]*?-->|<\?[\s\S]*?\?>|<!\[CDATA\[[\s\S]*?\]\]>|<\/([^\s>]+)\s*>|<([^\s/>!?]+)((?:\s+[^\s=/>]+\s*=\s*(?:"[^"]*"|'[^']*'))*)\s*(\/?)>/y;
const ATTRIBUTE = /([^\s=/>]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;

/** A small XML reader for manifests: elements, attributes and comments. Throws on malformed input. */
export function parseXml(text) {
	const root = { name: '#document', attrs: {}, children: [] };
	const stack = [root];
	let at = 0;
	while ((at = text.indexOf('<', at)) !== -1) {
		TOKEN.lastIndex = at;
		const match = TOKEN.exec(text);
		if (!match) throw new Error(`malformed XML near offset ${at}`);
		at = TOKEN.lastIndex;
		const [token, closing, opening, rawAttributes, selfClosing] = match;
		if (closing !== undefined) {
			const open = stack.pop();
			if (stack.length === 0 || open.name !== closing) {
				throw new Error(`unexpected </${closing}>`);
			}
		} else if (opening !== undefined) {
			const attrs = {};
			for (const attribute of rawAttributes.matchAll(ATTRIBUTE)) {
				attrs[attribute[1]] = decode(attribute[2] ?? attribute[3]);
			}
			const element = { name: opening, attrs, children: [] };
			stack.at(-1).children.push(element);
			if (!selfClosing) stack.push(element);
		} else if (!token.startsWith('<!--') && !token.startsWith('<?') && !token.startsWith('<![CDATA[')) {
			throw new Error(`malformed XML near offset ${at}`);
		}
	}
	if (stack.length !== 1) throw new Error(`unclosed <${stack.at(-1).name}>`);
	const manifest = root.children.find((child) => child.name === 'manifest');
	if (!manifest) throw new Error('no <manifest> element');
	return manifest;
}

const childrenNamed = (element, name) => element.children.filter((child) => child.name === name);

/**
 * Audits a merged manifest text. Returns { errors, summary }: `errors` is empty when it passes.
 * options: { allowedPermissions, expectVersionName, expectVersionCode, allowDebuggable }
 */
export function auditManifest(text, options = {}) {
	const errors = [];
	const manifest = parseXml(text);
	const allowedPermissions = new Set([...ALLOWED_PERMISSIONS, ...(options.allowedPermissions ?? [])]);

	const pkg = manifest.attrs.package;
	if (pkg !== EXPECTED_PACKAGE) {
		errors.push(`package is "${pkg}", expected "${EXPECTED_PACKAGE}"`);
	}

	const permissions = manifest.children
		.filter((child) => child.name.startsWith('uses-permission'))
		.map((child) => child.attrs['android:name'] ?? '(no android:name)');
	for (const permission of permissions) {
		if (!allowedPermissions.has(permission)) {
			errors.push(`uses-permission "${permission}" is not in the allow-list (${[...allowedPermissions].join(', ')})`);
		}
	}

	const application = childrenNamed(manifest, 'application')[0];
	if (!application) {
		errors.push('no <application> element');
		return { errors, summary: { permissions } };
	}
	if (application.attrs['android:allowBackup'] !== 'false') {
		errors.push(`<application android:allowBackup> is "${application.attrs['android:allowBackup'] ?? '(unset, defaults to true)'}", must be "false"`);
	}
	if (application.attrs['android:debuggable'] === 'true' && !options.allowDebuggable) {
		errors.push('<application android:debuggable="true">: audit a release manifest (or pass --allow-debuggable)');
	}

	const exported = [];
	for (const component of application.children.filter((child) => COMPONENT_TAGS.has(child.name))) {
		const name = component.attrs['android:name'] ?? '(no android:name)';
		const value = component.attrs['android:exported'];
		const hasFilter = childrenNamed(component, 'intent-filter').length > 0;
		const isExported = value === undefined ? hasFilter : value !== 'false';
		if (isExported) {
			exported.push(name);
			if (!ALLOWED_EXPORTED.includes(name)) {
				errors.push(`<${component.name} ${name}> is exported but not in the allow-list (${ALLOWED_EXPORTED.join(', ')})`);
			}
		}
	}

	// Activities only: the library looks the component up as an activity, a <service> of that name would not do.
	const declared = childrenNamed(application, 'activity').map((component) => component.attrs['android:name']);
	for (const name of REQUIRED_COMPONENTS) {
		if (!declared.includes(name)) {
			errors.push(`<activity ${name}> is missing: androidbrowserhelper needs it at launch (the app would crash on start)`);
		}
	}

	const webFilters = [];
	for (const component of application.children.filter((child) => COMPONENT_TAGS.has(child.name))) {
		for (const filter of childrenNamed(component, 'intent-filter')) {
			const data = childrenNamed(filter, 'data');
			if (!data.some((d) => WEB_SCHEMES.has(d.attrs['android:scheme']))) continue;
			const label = `${component.attrs['android:name']} intent-filter`;
			webFilters.push(label);
			if (!data.some((d) => d.attrs['android:host'])) {
				errors.push(`${label} matches web URLs without a host`);
			}
			const pathData = data.filter((d) => PATH_ATTRIBUTES.some((attribute) => d.attrs[attribute] !== undefined));
			if (pathData.length === 0) {
				errors.push(`${label} matches web URLs without any path restriction (android:path / android:pathPattern)`);
			}
			for (const d of data) {
				const scheme = d.attrs['android:scheme'];
				if (scheme !== undefined && scheme !== 'https') {
					errors.push(`${label}: android:scheme="${scheme}" is not https`);
				}
				const host = d.attrs['android:host'];
				if (host !== undefined && host !== EXPECTED_WEB_HOST) {
					errors.push(`${label}: android:host="${host}" is not ${EXPECTED_WEB_HOST}`);
				}
				const exact = d.attrs['android:path'];
				if (exact !== undefined && !EXPECTED_WEB_PATHS.includes(exact)) {
					errors.push(`${label}: android:path="${exact}" is not one of ${EXPECTED_WEB_PATHS.join(', ')}`);
				}
				const pattern = d.attrs['android:pathPattern'];
				if (pattern !== undefined && !EXPECTED_WEB_PATH_PATTERNS.includes(pattern)) {
					errors.push(`${label}: android:pathPattern="${pattern}" is not one of ${EXPECTED_WEB_PATH_PATTERNS.join(', ')}`);
				}
			}
			for (const d of pathData) {
				for (const attribute of PATH_ATTRIBUTES) {
					const value = d.attrs[attribute];
					if (value === undefined) continue;
					// android:path is an exact match: "/" is the home page only, not every path.
					if (attribute === 'android:pathPrefix' || attribute === 'android:pathSuffix' || attribute === 'android:pathAdvancedPattern') {
						errors.push(`${label}: ${attribute}="${value}" is too broad, use exact android:path entries`);
					} else if (attribute === 'android:pathPattern' && isCatchAllPattern(value)) {
						errors.push(`${label}: ${attribute}="${value}" matches every path`);
					} else if (attribute === 'android:path' && !value.startsWith('/')) {
						errors.push(`${label}: ${attribute}="${value}" must start with "/"`);
					}
				}
			}
		}
	}

	if (options.expectVersionName !== undefined && manifest.attrs['android:versionName'] !== options.expectVersionName) {
		errors.push(`android:versionName is "${manifest.attrs['android:versionName']}", expected "${options.expectVersionName}"`);
	}
	if (options.expectVersionCode !== undefined && manifest.attrs['android:versionCode'] !== String(options.expectVersionCode)) {
		errors.push(`android:versionCode is "${manifest.attrs['android:versionCode']}", expected "${options.expectVersionCode}"`);
	}

	return {
		errors,
		summary: {
			package: pkg,
			versionName: manifest.attrs['android:versionName'],
			versionCode: manifest.attrs['android:versionCode'],
			permissions,
			exported,
			webFilters
		}
	};
}

class UsageError extends Error {}

export function parseArgs(argv) {
	const options = { manifest: undefined, variant: 'release', allowedPermissions: [], allowDebuggable: false };
	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i];
		const value = () => {
			const next = argv[++i];
			if (next === undefined) throw new UsageError(`missing value for ${arg}`);
			return next;
		};
		switch (arg) {
			case '--variant':
				options.variant = value();
				break;
			case '--expect-version-name':
				options.expectVersionName = value();
				break;
			case '--expect-version-code': {
				const code = value();
				if (!/^[1-9][0-9]*$/.test(code)) throw new UsageError(`--expect-version-code needs a positive integer, got "${code}"`);
				options.expectVersionCode = Number(code);
				break;
			}
			case '--allow-permission':
				options.allowedPermissions.push(value());
				break;
			case '--allow-debuggable':
				options.allowDebuggable = true;
				break;
			default:
				if (arg.startsWith('--')) throw new UsageError(`unknown option ${arg}`);
				if (options.manifest) throw new UsageError('only one manifest path is accepted');
				options.manifest = arg;
		}
	}
	return options;
}

/** The single merged AndroidManifest.xml of `variant` below android/app/build. */
export function findMergedManifest(androidDir, variant) {
	if (!/^[A-Za-z0-9]+$/.test(variant)) throw new UsageError(`invalid variant "${variant}"`);
	const base = path.join(androidDir, 'app', 'build', 'intermediates', 'merged_manifests', variant);
	const found = [];
	const walk = (dir) => {
		let entries;
		try {
			entries = readdirSync(dir);
		} catch {
			return;
		}
		for (const entry of entries) {
			const full = path.join(dir, entry);
			if (statSync(full).isDirectory()) walk(full);
			else if (entry === 'AndroidManifest.xml') found.push(full);
		}
	};
	walk(base);
	if (found.length !== 1) {
		throw new Error(
			found.length === 0
				? `no merged manifest under ${base}: run ./gradlew bundleRelease first`
				: `expected one merged manifest under ${base}, found ${found.length}: ${found.join(', ')}`
		);
	}
	return found[0];
}

function main() {
	let options;
	try {
		options = parseArgs(process.argv.slice(2));
	} catch (error) {
		console.error(`audit-manifest: ${error.message}`);
		process.exit(2);
	}
	let file;
	let text;
	try {
		const androidDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
		file = options.manifest ?? findMergedManifest(androidDir, options.variant);
		text = readFileSync(file, 'utf8');
	} catch (error) {
		console.error(`audit-manifest: ${error.message}`);
		process.exit(error instanceof UsageError ? 2 : 1);
	}
	let result;
	try {
		result = auditManifest(text, options);
	} catch (error) {
		console.error(`audit-manifest: cannot read ${file}: ${error.message}`);
		process.exit(1);
	}
	const { errors, summary } = result;
	console.log(`audit-manifest: ${file}`);
	console.log(`  package ${summary.package} versionName ${summary.versionName} versionCode ${summary.versionCode}`);
	console.log(`  permissions: ${summary.permissions?.length ? summary.permissions.join(', ') : '(none)'}`);
	console.log(`  exported components: ${summary.exported?.length ? summary.exported.join(', ') : '(none)'}`);
	console.log(`  web intent-filters: ${summary.webFilters?.length ? summary.webFilters.join(', ') : '(none)'}`);
	if (errors.length > 0) {
		for (const error of errors) console.error(`::error::audit-manifest: ${error}`);
		process.exit(1);
	}
	console.log('audit-manifest: OK');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	main();
}
