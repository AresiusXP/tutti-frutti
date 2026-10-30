import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import {
	ALLOWED_EXPORTED,
	ALLOWED_PERMISSIONS,
	auditManifest,
	EXPECTED_PACKAGE,
	findMergedManifest,
	parseArgs,
	parseXml,
	REQUIRED_COMPONENTS
} from './audit-manifest.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const script = path.join(here, 'audit-manifest.mjs');
// The merged RELEASE manifest of a real `./gradlew bundleRelease` (versionName 1.6.0, code 1).
const GOOD = readFileSync(path.join(here, 'fixtures', 'merged-release.xml'), 'utf8');

function mutate(search, replacement) {
	assert.ok(GOOD.includes(search), `fixture no longer contains: ${search}`);
	return GOOD.replace(search, replacement);
}

const errorsOf = (text, options) => auditManifest(text, options).errors;

test('the real merged manifest passes', () => {
	const { errors, summary } = auditManifest(GOOD);
	assert.deepEqual(errors, []);
	assert.equal(summary.package, EXPECTED_PACKAGE);
	assert.deepEqual(summary.permissions, ALLOWED_PERMISSIONS);
	assert.deepEqual([...summary.exported].sort(), [...ALLOWED_EXPORTED].sort());
	assert.equal(summary.webFilters.length, 1);
});

test('a missing ManageDataLauncherActivity fails: the app would crash on launch', () => {
	const name = 'com.google.androidbrowserhelper.trusted.ManageDataLauncherActivity';
	assert.ok(REQUIRED_COMPONENTS.includes(name));
	const without = mutate(`android:name="${name}"`, 'android:name="com.example.Other"');
	assert.ok(errorsOf(without).some((e) => e.includes(name) && e.includes('crash')));
});

test('ManageDataLauncherActivity declared as something other than an <activity> fails too', () => {
	const name = 'com.google.androidbrowserhelper.trusted.ManageDataLauncherActivity';
	const start = GOOD.indexOf(`<activity\n            android:name="${name}"`);
	assert.ok(start >= 0, 'fixture declares the activity on its own line');
	const end = GOOD.indexOf('</activity>', start) + '</activity>'.length;
	const asService = GOOD.slice(0, start) + `<service android:name="${name}" android:exported="false" />` + GOOD.slice(end);
	assert.ok(errorsOf(asService).some((e) => e.includes(name)));
});

test('parseXml reads attributes, comments and self-closing elements', () => {
	const root = parseXml(`<?xml version="1.0"?><!-- <fake a="1"/> -->
		<manifest a='x &amp; y'><application><data android:path="/a>b"/></application></manifest>`);
	assert.equal(root.attrs.a, 'x & y');
	assert.equal(root.children[0].children[0].attrs['android:path'], '/a>b');
});

test('parseXml rejects malformed input', () => {
	assert.throws(() => parseXml('<manifest><application></manifest>'), /unexpected/);
	assert.throws(() => parseXml('<manifest><application></application>'), /unclosed/);
	assert.throws(() => parseXml('<manifest a=b></manifest>'), /malformed/);
	assert.throws(() => parseXml('not xml'), /no <manifest>/);
	assert.throws(() => parseXml('<other/>'), /no <manifest>/);
});

test('any permission outside the allow-list fails, INTERNET included', () => {
	const internet = mutate('<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC', '<uses-permission android:name="android.permission.INTERNET" />\n<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC');
	const errors = errorsOf(internet);
	assert.equal(errors.length, 1);
	assert.match(errors[0], /uses-permission "android.permission.INTERNET" is not in the allow-list/);

	const sdk23 = mutate('<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC', '<uses-permission-sdk-23 android:name="android.permission.CAMERA" />\n<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC');
	assert.match(errorsOf(sdk23).join('\n'), /android.permission.CAMERA/);
});

test('--allow-permission extends the allow-list explicitly', () => {
	const text = mutate('<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC', '<uses-permission android:name="android.permission.VIBRATE" />\n<uses-permission android:name="xyz.aresius.tuttifrutti.DYNAMIC');
	assert.equal(errorsOf(text).length, 1);
	assert.deepEqual(errorsOf(text, { allowedPermissions: ['android.permission.VIBRATE'] }), []);
});

test('a manifest without permissions at all passes', () => {
	const text = GOOD.replace(/<uses-permission[^>]*\/>/g, '');
	assert.deepEqual(errorsOf(text), []);
});

test('allowBackup must be exactly "false"', () => {
	assert.match(errorsOf(mutate('android:allowBackup="false"', 'android:allowBackup="true"')).join('\n'), /allowBackup> is "true"/);
	assert.match(errorsOf(mutate('android:allowBackup="false"', '')).join('\n'), /unset, defaults to true/);
});

test('a debuggable manifest fails unless explicitly allowed', () => {
	const debuggable = mutate('android:allowBackup="false"', 'android:allowBackup="false" android:debuggable="true"');
	assert.match(errorsOf(debuggable).join('\n'), /debuggable/);
	assert.deepEqual(errorsOf(debuggable, { allowDebuggable: true }), []);
});

test('the package id is permanent', () => {
	assert.match(errorsOf(GOOD.replace('package="xyz.aresius.tuttifrutti"', 'package="com.example.app"')).join('\n'), /package is "com.example.app"/);
});

test('a web intent-filter without a path restriction fails', () => {
	const noPaths = GOOD.replace(/\s*<data android:path[^>]*\/>/g, '').replace(/\s*<data android:pathPattern[^>]*\/>/g, '');
	assert.notEqual(noPaths, GOOD);
	assert.match(errorsOf(noPaths).join('\n'), /without any path restriction/);
});

test('a web intent-filter without a host fails', () => {
	const noHost = GOOD.replace(/\s*<data android:host="tuttifrutti.aresius.xyz" \/>/, '');
	assert.notEqual(noHost, GOOD);
	assert.match(errorsOf(noHost).join('\n'), /without a host/);
});

test('catch-all and prefix path restrictions fail', () => {
	const prefix = mutate('<data android:path="/" />', '<data android:pathPrefix="/" />');
	assert.match(errorsOf(prefix).join('\n'), /pathPrefix="\/" is too broad/);
	for (const pattern of ['.*', '/.*', '/..*', '*']) {
		const text = mutate('<data android:pathPattern="/...." />', `<data android:pathPattern="${pattern}" />`);
		assert.match(errorsOf(text).join('\n'), /matches every path/, pattern);
	}
	assert.match(errorsOf(mutate('<data android:path="/" />', '<data android:pathAdvancedPattern="/.*" />')).join('\n'), /too broad/);
	assert.match(errorsOf(mutate('<data android:path="/" />', '<data android:path="rooms" />')).join('\n'), /must start with/);
});

test('exact paths and the fixed-length room pattern are not catch-alls', () => {
	assert.deepEqual(errorsOf(GOOD), []);
});

test('the verified web filter matches only https, this host and the known pages', () => {
	const http = GOOD.replace(
		/(<data android:scheme="https" \/>)(\s*<data android:host=)/,
		'<data android:scheme="http" />$2'
	);
	assert.notEqual(http, GOOD);
	assert.match(errorsOf(http).join('\n'), /android:scheme="http" is not https/);
	const host = mutate('<data android:host="tuttifrutti.aresius.xyz" />', '<data android:host="example.com" />');
	assert.match(errorsOf(host).join('\n'), /android:host="example.com" is not tuttifrutti.aresius.xyz/);
	const path = mutate('<data android:path="/stats" />', '<data android:path="/api" />');
	assert.match(errorsOf(path).join('\n'), /android:path="\/api" is not one of/);
	const longer = mutate('<data android:pathPattern="/...." />', '<data android:pathPattern="/....." />');
	assert.match(errorsOf(longer).join('\n'), /android:pathPattern="\/\.\.\.\.\." is not one of/);
});

test('the <queries> https intent is not an intent-filter and is ignored', () => {
	assert.ok(GOOD.includes('<queries>'));
	assert.deepEqual(errorsOf(GOOD), []);
});

test('a new exported component fails', () => {
	const extra = GOOD.replace(
		'</application>',
		'<activity android:name="com.example.Evil" android:exported="true" /></application>'
	);
	assert.match(errorsOf(extra).join('\n'), /Evil> is exported but not in the allow-list/);

	// no android:exported but an intent-filter: exported by default for older targets, and fails
	const implicit = GOOD.replace(
		'</application>',
		'<service android:name="com.example.Svc"><intent-filter><action android:name="x" /></intent-filter></service></application>'
	);
	assert.match(errorsOf(implicit).join('\n'), /Svc> is exported/);

	const notExported = GOOD.replace('</application>', '<activity android:name="com.example.Ok" android:exported="false" /></application>');
	assert.deepEqual(errorsOf(notExported), []);
});

test('the shortcut trampoline of androidbrowserhelper must stay removed', () => {
	assert.ok(!GOOD.includes('ShortcutTrampolineActivity'));
	const trampoline = GOOD.replace(
		'</application>',
		'<activity android:name="com.google.androidbrowserhelper.trusted.ShortcutTrampolineActivity" android:exported="true" /></application>'
	);
	assert.match(errorsOf(trampoline).join('\n'), /ShortcutTrampolineActivity> is exported/);
});

test('version expectations', () => {
	assert.deepEqual(errorsOf(GOOD, { expectVersionName: '1.6.0', expectVersionCode: 1 }), []);
	assert.match(errorsOf(GOOD, { expectVersionName: '1.5.0' }).join('\n'), /versionName is "1.6.0", expected "1.5.0"/);
	assert.match(errorsOf(GOOD, { expectVersionCode: 2 }).join('\n'), /versionCode is "1", expected "2"/);
});

test('parseArgs', () => {
	const options = parseArgs(['out.xml', '--variant', 'debug', '--expect-version-name', '1.0.0', '--expect-version-code', '7', '--allow-permission', 'a.B', '--allow-debuggable']);
	assert.equal(options.manifest, 'out.xml');
	assert.equal(options.variant, 'debug');
	assert.equal(options.expectVersionName, '1.0.0');
	assert.equal(options.expectVersionCode, 7);
	assert.deepEqual(options.allowedPermissions, ['a.B']);
	assert.equal(options.allowDebuggable, true);
	assert.equal(parseArgs([]).variant, 'release');
	assert.throws(() => parseArgs(['--expect-version-code', '0']), /positive integer/);
	assert.throws(() => parseArgs(['--variant']), /missing value/);
	assert.throws(() => parseArgs(['--what']), /unknown option/);
	assert.throws(() => parseArgs(['a.xml', 'b.xml']), /only one manifest/);
});

test('findMergedManifest locates the single merged manifest of a variant', () => {
	const dir = mkdtempSync(path.join(tmpdir(), 'audit-manifest-'));
	try {
		assert.throws(() => findMergedManifest(dir, 'release'), /run \.\/gradlew bundleRelease first/);
		const nested = path.join(dir, 'app', 'build', 'intermediates', 'merged_manifests', 'release', 'processReleaseManifest');
		mkdirSync(nested, { recursive: true });
		writeFileSync(path.join(nested, 'AndroidManifest.xml'), GOOD);
		assert.equal(findMergedManifest(dir, 'release'), path.join(nested, 'AndroidManifest.xml'));
		const other = path.join(dir, 'app', 'build', 'intermediates', 'merged_manifests', 'release', 'other');
		mkdirSync(other, { recursive: true });
		writeFileSync(path.join(other, 'AndroidManifest.xml'), GOOD);
		assert.throws(() => findMergedManifest(dir, 'release'), /found 2/);
		assert.throws(() => findMergedManifest(dir, '../etc'), /invalid variant/);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

function cli(args, manifestText) {
	const dir = mkdtempSync(path.join(tmpdir(), 'audit-manifest-cli-'));
	try {
		const file = path.join(dir, 'AndroidManifest.xml');
		writeFileSync(file, manifestText);
		return spawnSync(process.execPath, [script, file, ...args], { encoding: 'utf8' });
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
}

test('CLI: exit 0 with a summary on a good manifest', () => {
	const result = cli(['--expect-version-name', '1.6.0', '--expect-version-code', '1'], GOOD);
	assert.equal(result.status, 0, result.stderr);
	assert.match(result.stdout, /permissions: xyz\.aresius\.tuttifrutti\.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION/);
	assert.match(result.stdout, /audit-manifest: OK/);
});

test('CLI: exit 1 and ::error:: lines on a bad manifest', () => {
	const bad = mutate('android:allowBackup="false"', 'android:allowBackup="true"');
	const result = cli([], bad);
	assert.equal(result.status, 1);
	assert.match(result.stderr, /::error::audit-manifest: <application android:allowBackup>/);
	assert.doesNotMatch(result.stdout, /audit-manifest: OK/);
});

test('CLI: exit 1 on an unreadable manifest, 2 on bad usage', () => {
	assert.equal(cli([], '<manifest><application>').status, 1);
	assert.equal(cli(['--bogus'], GOOD).status, 2);
});
