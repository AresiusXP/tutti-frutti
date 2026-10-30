#!/usr/bin/env node
// Validates the version of the Android app before it is built/published.
//
//   versionName  = appVersion in helm/tutti-frutti/Chart.yaml (bumped by release-please)
//   versionCode  = the single integer in android/version-code.txt (bumped by hand per Play release)
//
// Usage:
//   node scripts/check-version.mjs [--chart PATH] [--code-file PATH]
//                                  [--min-exclusive N] [--expect-version X.Y.Z]
//
//   --min-exclusive N     fail unless versionCode > N (N = versionCode of the previous release)
//   --expect-version V    fail unless versionName equals V (a leading "v" is ignored, so a
//                         release tag can be passed as is)
//
// Prints "versionName=..." and "versionCode=..." on success (append to $GITHUB_OUTPUT as is).
// Exit code: 0 ok, 1 check failed, 2 bad usage. Pure node, no dependencies.
//
// app/build.gradle implements the same two reads (and fails the build the same way); keep the
// patterns in sync.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const MAX_VERSION_CODE = 2_100_000_000; // Google Play's upper bound

const SEMVER = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;
const APP_VERSION_LINE = /^appVersion:\s*"?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)"?\s*(?:#.*)?$/;

export function isSemver(value) {
	return typeof value === 'string' && SEMVER.test(value);
}

/** The appVersion of a Chart.yaml text. Throws with a clear message when it is missing/invalid. */
export function parseAppVersion(chartText) {
	for (const line of chartText.split(/\r?\n/)) {
		const match = APP_VERSION_LINE.exec(line);
		if (match) {
			if (!isSemver(match[1])) {
				throw new Error(`appVersion "${match[1]}" in Chart.yaml is not a semantic version (X.Y.Z)`);
			}
			return match[1];
		}
	}
	throw new Error(`no 'appVersion: "X.Y.Z"' line found in Chart.yaml`);
}

/** The versionCode of a version-code.txt text: one positive integer, nothing else. */
export function parseVersionCode(text) {
	const value = text.trim();
	if (!/^[1-9][0-9]{0,9}$/.test(value)) {
		throw new Error(`version-code.txt must hold a single positive integer, got "${value.slice(0, 40)}"`);
	}
	const code = Number(value);
	if (code > MAX_VERSION_CODE) {
		throw new Error(`versionCode ${code} exceeds Google Play's maximum of ${MAX_VERSION_CODE}`);
	}
	return code;
}

/**
 * Runs every check. Returns { versionName, versionCode }; throws an Error describing the first
 * violated rule.
 */
export function checkVersion({ chartText, codeText, minExclusive, expectVersion }) {
	const versionName = parseAppVersion(chartText);
	const versionCode = parseVersionCode(codeText);
	if (minExclusive !== undefined) {
		if (!Number.isInteger(minExclusive) || minExclusive < 0) {
			throw new Error(`--min-exclusive must be a non-negative integer, got ${minExclusive}`);
		}
		if (versionCode <= minExclusive) {
			throw new Error(
				`versionCode ${versionCode} must be greater than the previous release's ${minExclusive}: bump android/version-code.txt`
			);
		}
	}
	if (expectVersion !== undefined) {
		const expected = expectVersion.replace(/^v/, '');
		if (!isSemver(expected)) {
			throw new Error(`--expect-version "${expectVersion}" is not a semantic version`);
		}
		if (versionName !== expected) {
			throw new Error(`Chart.yaml appVersion ${versionName} does not match the expected version ${expected}`);
		}
	}
	return { versionName, versionCode };
}

class UsageError extends Error {}

export function parseArgs(argv, here = path.dirname(fileURLToPath(import.meta.url))) {
	const options = {
		chart: path.resolve(here, '..', '..', 'helm', 'tutti-frutti', 'Chart.yaml'),
		codeFile: path.resolve(here, '..', 'version-code.txt'),
		minExclusive: undefined,
		expectVersion: undefined
	};
	for (let i = 0; i < argv.length; i++) {
		const flag = argv[i];
		const value = argv[++i];
		if (value === undefined) throw new UsageError(`missing value for ${flag}`);
		switch (flag) {
			case '--chart':
				options.chart = path.resolve(value);
				break;
			case '--code-file':
				options.codeFile = path.resolve(value);
				break;
			case '--min-exclusive':
				if (!/^[0-9]+$/.test(value)) throw new UsageError(`--min-exclusive needs a non-negative integer, got "${value}"`);
				options.minExclusive = Number(value);
				break;
			case '--expect-version':
				options.expectVersion = value;
				break;
			default:
				throw new UsageError(`unknown option ${flag}`);
		}
	}
	return options;
}

function main() {
	let options;
	try {
		options = parseArgs(process.argv.slice(2));
	} catch (error) {
		console.error(`check-version: ${error.message}`);
		console.error('usage: check-version.mjs [--chart PATH] [--code-file PATH] [--min-exclusive N] [--expect-version X.Y.Z]');
		process.exit(2);
	}
	try {
		const { versionName, versionCode } = checkVersion({
			chartText: readFileSync(options.chart, 'utf8'),
			codeText: readFileSync(options.codeFile, 'utf8'),
			minExclusive: options.minExclusive,
			expectVersion: options.expectVersion
		});
		console.log(`versionName=${versionName}`);
		console.log(`versionCode=${versionCode}`);
	} catch (error) {
		console.error(`check-version: ${error.message}`);
		process.exit(1);
	}
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	main();
}
