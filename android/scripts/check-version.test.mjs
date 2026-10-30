import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import {
	checkVersion,
	isSemver,
	MAX_VERSION_CODE,
	parseAppVersion,
	parseArgs,
	parseVersionCode
} from './check-version.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const script = path.join(here, 'check-version.mjs');

const CHART = `apiVersion: v2
name: tutti-frutti
# version and appVersion are bumped by release-please
version: 1.4.0 # x-release-please-version
appVersion: "1.4.0" # x-release-please-version
`;

test('isSemver accepts X.Y.Z and pre-releases, rejects the rest', () => {
	for (const ok of ['0.0.1', '1.4.0', '10.20.30', '1.0.0-rc.1', '1.0.0-alpha']) {
		assert.equal(isSemver(ok), true, ok);
	}
	for (const bad of ['1', '1.4', '1.4.0.1', 'v1.4.0', '01.4.0', '1.04.0', '1.4.0-', '1.4.0+build', '', ' 1.4.0', 'a.b.c', null, undefined, 140]) {
		assert.equal(isSemver(bad), false, String(bad));
	}
});

test('parseAppVersion reads the release-please appVersion line', () => {
	assert.equal(parseAppVersion(CHART), '1.4.0');
	assert.equal(parseAppVersion('appVersion: 2.0.1\n'), '2.0.1');
	assert.equal(parseAppVersion('appVersion: "3.1.0-rc.2"\r\nversion: 3.1.0\r\n'), '3.1.0-rc.2');
});

test('parseAppVersion ignores version: and indented/commented lines', () => {
	assert.throws(() => parseAppVersion('version: 1.4.0\n'), /no 'appVersion/);
	assert.throws(() => parseAppVersion('  appVersion: "1.4.0"\n'), /no 'appVersion/);
	assert.throws(() => parseAppVersion('# appVersion: "1.4.0"\n'), /no 'appVersion/);
});

test('parseAppVersion fails clearly on missing or malformed values', () => {
	assert.throws(() => parseAppVersion(''), /no 'appVersion/);
	assert.throws(() => parseAppVersion('appVersion: "1.4"\n'), /no 'appVersion/);
	assert.throws(() => parseAppVersion('appVersion: latest\n'), /no 'appVersion/);
	assert.throws(() => parseAppVersion('appVersion: "01.4.0"\n'), /not a semantic version/);
});

test('parseVersionCode accepts a single positive integer', () => {
	assert.equal(parseVersionCode('1\n'), 1);
	assert.equal(parseVersionCode('42'), 42);
	assert.equal(parseVersionCode('  7  \n\n'), 7);
	assert.equal(parseVersionCode(String(MAX_VERSION_CODE)), MAX_VERSION_CODE);
});

test('parseVersionCode rejects everything else', () => {
	for (const bad of ['', '\n', '0', '00', '01', '-1', '1.5', '1 2', '1\n2', '1\n# comment', 'abc', '1a', '+1', '0x10', '1e3', String(MAX_VERSION_CODE + 1), '12345678901']) {
		assert.throws(() => parseVersionCode(bad), /version-code|maximum/, JSON.stringify(bad));
	}
});

test('checkVersion returns the name and the code', () => {
	assert.deepEqual(checkVersion({ chartText: CHART, codeText: '3\n' }), { versionName: '1.4.0', versionCode: 3 });
});

test('--min-exclusive enforces versionCode > N', () => {
	const run = (min) => checkVersion({ chartText: CHART, codeText: '5\n', minExclusive: min });
	assert.equal(run(4).versionCode, 5);
	assert.equal(run(0).versionCode, 5);
	assert.throws(() => run(5), /must be greater than the previous release's 5/);
	assert.throws(() => run(6), /must be greater than/);
	assert.throws(() => run(-1), /non-negative/);
	assert.throws(() => run(1.5), /non-negative/);
});

test('--expect-version compares with the Chart.yaml version, with or without a leading v', () => {
	assert.equal(checkVersion({ chartText: CHART, codeText: '1', expectVersion: 'v1.4.0' }).versionName, '1.4.0');
	assert.equal(checkVersion({ chartText: CHART, codeText: '1', expectVersion: '1.4.0' }).versionName, '1.4.0');
	assert.throws(() => checkVersion({ chartText: CHART, codeText: '1', expectVersion: 'v1.4.1' }), /does not match/);
	assert.throws(() => checkVersion({ chartText: CHART, codeText: '1', expectVersion: 'main' }), /not a semantic version/);
});

test('parseArgs defaults to the repository files and validates options', () => {
	const options = parseArgs([], '/repo/android/scripts');
	assert.equal(options.chart, path.resolve('/repo/helm/tutti-frutti/Chart.yaml'));
	assert.equal(options.codeFile, path.resolve('/repo/android/version-code.txt'));
	assert.equal(options.minExclusive, undefined);
	assert.equal(parseArgs(['--min-exclusive', '12', '--expect-version', 'v1.2.3']).minExclusive, 12);
	assert.throws(() => parseArgs(['--min-exclusive', 'x']), /non-negative integer/);
	assert.throws(() => parseArgs(['--min-exclusive']), /missing value/);
	assert.throws(() => parseArgs(['--nope', '1']), /unknown option/);
});

function cli(args, files) {
	const dir = mkdtempSync(path.join(tmpdir(), 'check-version-'));
	try {
		const chart = path.join(dir, 'Chart.yaml');
		const code = path.join(dir, 'version-code.txt');
		writeFileSync(chart, files.chart);
		writeFileSync(code, files.code);
		return spawnSync(process.execPath, [script, '--chart', chart, '--code-file', code, ...args], { encoding: 'utf8' });
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
}

test('CLI prints key=value lines and exits 0', () => {
	const result = cli(['--min-exclusive', '2', '--expect-version', 'v1.4.0'], { chart: CHART, code: '3\n' });
	assert.equal(result.status, 0, result.stderr);
	assert.equal(result.stdout, 'versionName=1.4.0\nversionCode=3\n');
});

test('CLI exits 1 with a clear message when a check fails', () => {
	const stale = cli(['--min-exclusive', '3'], { chart: CHART, code: '3\n' });
	assert.equal(stale.status, 1);
	assert.match(stale.stderr, /check-version: versionCode 3 must be greater/);
	const bad = cli([], { chart: CHART, code: 'abc\n' });
	assert.equal(bad.status, 1);
	assert.match(bad.stderr, /single positive integer/);
	const noVersion = cli([], { chart: 'name: x\n', code: '1\n' });
	assert.equal(noVersion.status, 1);
	assert.match(noVersion.stderr, /no 'appVersion/);
});

test('CLI exits 2 on bad usage', () => {
	const result = spawnSync(process.execPath, [script, '--bogus', 'x'], { encoding: 'utf8' });
	assert.equal(result.status, 2);
	assert.match(result.stderr, /unknown option/);
});

test('the repository files are valid right now', () => {
	const out = execFileSync(process.execPath, [script], { encoding: 'utf8' });
	assert.match(out, /^versionName=\d+\.\d+\.\d+.*\nversionCode=[1-9][0-9]*\n$/);
});
