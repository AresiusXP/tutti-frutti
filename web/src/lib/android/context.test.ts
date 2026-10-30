// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
	ANDROID_MARKER_KEY,
	ANDROID_REFERRER,
	isAndroidApp,
	isAndroidReferrer,
	startAndroidContext,
	type AndroidEnv
} from './context';

function memory() {
	const data = new Map<string, string>();
	return {
		data,
		getItem: (k: string) => data.get(k) ?? null,
		setItem: (k: string, v: string) => void data.set(k, v)
	};
}

const env = (over: Partial<AndroidEnv> = {}): AndroidEnv => ({
	referrer: '',
	session: memory(),
	appDisplayMode: () => false,
	...over
});

describe('isAndroidReferrer', () => {
	it.each([
		['the bare package', 'android-app://xyz.aresius.tuttifrutti'],
		['with a trailing slash', 'android-app://xyz.aresius.tuttifrutti/'],
		[
			'with an android-app path',
			'android-app://xyz.aresius.tuttifrutti/https/tuttifrutti.aresius.xyz/'
		]
	])('accepts %s', (_name, referrer) => {
		expect(isAndroidReferrer(referrer)).toBe(true);
	});

	it.each([
		['a longer package name', 'android-app://xyz.aresius.tuttifrutti.evil'],
		['a package that only extends it', 'android-app://xyz.aresius.tuttifruttix'],
		['a userinfo trick', 'android-app://xyz.aresius.tuttifrutti@evil.example'],
		['a port', 'android-app://xyz.aresius.tuttifrutti:8080'],
		['a query without a path', 'android-app://xyz.aresius.tuttifrutti?x=1'],
		['a prefix of the package', 'android-app://xyz.aresius'],
		['another app', 'android-app://com.google.android.googlequicksearchbox'],
		['another app that mentions ours later', 'android-app://com.evil/xyz.aresius.tuttifrutti'],
		['a web page that mentions it', 'https://evil.example/android-app://xyz.aresius.tuttifrutti'],
		['the same text after a space', ' android-app://xyz.aresius.tuttifrutti'],
		['the website itself', 'https://tuttifrutti.aresius.xyz/'],
		['Google sign-in', 'https://accounts.google.com/'],
		['an empty referrer', ''],
		['nothing', undefined],
		['null', null]
	])('rejects %s', (_name, referrer) => {
		expect(isAndroidReferrer(referrer)).toBe(false);
	});

	it('is built from the permanent package id', () => {
		expect(ANDROID_REFERRER).toBe('android-app://xyz.aresius.tuttifrutti');
	});
});

describe('the marker', () => {
	it('is set when the app starts from the Android launcher, in sessionStorage as tf.android = 1', () => {
		const e = env({ referrer: 'android-app://xyz.aresius.tuttifrutti' });
		startAndroidContext(e);
		expect(ANDROID_MARKER_KEY).toBe('tf.android');
		expect((e.session as ReturnType<typeof memory>).data).toEqual(new Map([['tf.android', '1']]));
		expect(isAndroidApp(e)).toBe(true);
	});

	it.each([
		'',
		'https://accounts.google.com/',
		'android-app://xyz.aresius.tuttifrutti.evil',
		'android-app://com.whatsapp'
	])('is not set for the referrer %j: the website stays as it is', (referrer) => {
		const e = env({ referrer });
		startAndroidContext(e);
		expect((e.session as ReturnType<typeof memory>).data.size).toBe(0);
		expect(isAndroidApp(e)).toBe(false);
	});

	it('survives a simulated reload and the way back from Google sign-in and the static pages (the referrer differs)', () => {
		const tab = memory(); // one tab: its sessionStorage outlives every document it loads
		startAndroidContext(env({ session: tab, referrer: 'android-app://xyz.aresius.tuttifrutti' }));
		for (const referrer of [
			'', // a reload of an app launched document keeps its referrer; a reload of another one has none
			'https://accounts.google.com/',
			'https://tuttifrutti.aresius.xyz/privacy',
			'https://tuttifrutti.aresius.xyz/terms',
			'https://tuttifrutti.aresius.xyz/delete-account'
		]) {
			const next = env({ session: tab, referrer });
			startAndroidContext(next); // the app start of the new document
			expect(isAndroidApp(next), referrer).toBe(true);
			expect(tab.data.get('tf.android')).toBe('1'); // never cleared
		}
	});

	it('is per tab: the website in another tab of the same browser keeps its links', () => {
		startAndroidContext(env({ referrer: 'android-app://xyz.aresius.tuttifrutti' })); // the TWA tab
		const otherTab = env({ referrer: 'https://www.google.com/' });
		startAndroidContext(otherTab);
		expect(isAndroidApp(otherTab)).toBe(false);
	});

	it('ignores anything but "1" and a localStorage flag, which would be shared with the website', () => {
		expect(
			isAndroidApp(env({ session: { getItem: () => 'true', setItem: () => undefined } }))
		).toBe(false);
		localStorage.setItem('tf.android', '1');
		expect(isAndroidApp()).toBe(false);
		localStorage.removeItem('tf.android');
	});

	describe('the real browser', () => {
		beforeEach(() => sessionStorage.clear());
		afterEach(() => {
			sessionStorage.clear();
			vi.restoreAllMocks();
		});

		it('reads document.referrer and sessionStorage by default', () => {
			vi.spyOn(document, 'referrer', 'get').mockReturnValue(
				'android-app://xyz.aresius.tuttifrutti'
			);
			expect(isAndroidApp()).toBe(false); // nothing written yet, and jsdom is not a standalone window
			startAndroidContext();
			expect(sessionStorage.getItem('tf.android')).toBe('1');
			expect(localStorage.getItem('tf.android')).toBeNull();
			vi.spyOn(document, 'referrer', 'get').mockReturnValue('https://accounts.google.com/');
			expect(isAndroidApp()).toBe(true);
		});

		it('is false on an ordinary page', () => {
			vi.spyOn(document, 'referrer', 'get').mockReturnValue('');
			startAndroidContext();
			expect(isAndroidApp()).toBe(false);
		});
	});
});

describe('the second signal: launched by the app in an app display mode', () => {
	const launched = 'android-app://xyz.aresius.tuttifrutti';

	it('counts when the marker could not be written (storage refused)', () => {
		const refusing = {
			getItem: () => {
				throw new DOMException('denied', 'SecurityError');
			},
			setItem: () => {
				throw new DOMException('denied', 'SecurityError');
			}
		};
		const e = env({ referrer: launched, session: refusing, appDisplayMode: () => true });
		expect(() => startAndroidContext(e)).not.toThrow();
		expect(isAndroidApp(e)).toBe(true);
	});

	it('counts without any storage at all', () => {
		expect(
			isAndroidApp(env({ referrer: launched, session: null, appDisplayMode: () => true }))
		).toBe(true);
	});

	it('needs the display mode: the bare referrer is not enough when the marker is missing', () => {
		expect(isAndroidApp(env({ referrer: launched, session: null }))).toBe(false);
	});

	it('does not hide anything for a plain installed PWA (no referrer, or another app)', () => {
		expect(isAndroidApp(env({ referrer: '', appDisplayMode: () => true }))).toBe(false);
		expect(
			isAndroidApp(env({ referrer: 'android-app://com.whatsapp', appDisplayMode: () => true }))
		).toBe(false);
		expect(
			isAndroidApp(
				env({ referrer: 'android-app://xyz.aresius.tuttifrutti.evil', appDisplayMode: () => true })
			)
		).toBe(false);
	});

	it('survives a browser that cannot answer matchMedia', () => {
		const e = env({
			referrer: launched,
			session: null,
			appDisplayMode: () => {
				throw new TypeError('matchMedia is not a function');
			}
		});
		expect(isAndroidApp(e)).toBe(false);
	});

	it('asks the browser for standalone and fullscreen (a TWA in immersive mode), nothing else', () => {
		const asked: string[] = [];
		sessionStorage.clear();
		vi.stubGlobal('matchMedia', (query: string) => {
			asked.push(query);
			return { matches: query === '(display-mode: fullscreen)' };
		});
		vi.spyOn(document, 'referrer', 'get').mockReturnValue(launched);
		try {
			expect(isAndroidApp()).toBe(true);
			expect(asked).toEqual(['(display-mode: standalone)', '(display-mode: fullscreen)']);
		} finally {
			vi.unstubAllGlobals();
			vi.restoreAllMocks();
		}
	});
});
