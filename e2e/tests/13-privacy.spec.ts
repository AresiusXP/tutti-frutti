import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import { createRoom } from '../src/flows';
import type { Locale } from '../src/messages';
import type { Player } from '../src/player';

test.describe.configure({ mode: 'parallel' });

/**
 * The privacy policy is the page linked from the Google Cloud consent screen (/privacy). It is a
 * static HTML page served by nginx, outside the SPA: plain HTML, no script, the language chosen by
 * the URL fragment. These specs check that it is reachable, readable and honest.
 */

/** What each language of the policy must show: its title, and one sentence that is only in that language. */
const POLICY: Record<Locale, { title: string; sentence: RegExp; lang: string }> = {
	es: { title: 'Política de privacidad', sentence: /Podés jugar sin cuenta/, lang: 'es' },
	en: { title: 'Privacy Policy', sentence: /You can play without an account/, lang: 'en' },
	nl: { title: 'Privacybeleid', sentence: /Je kunt spelen zonder account/, lang: 'nl' }
};

/** The one visible language block of the policy page. */
const visibleDoc = (player: Player) => player.page.locator('section.doc:visible');

for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`the link on Home opens the policy in the app's language, and the way back works (${locale})`, async ({
		app
	}) => {
		const ana = await app.player('Ana', { locale });
		const expected = POLICY[locale];
		await ana.home.open();

		await test.step('a quiet 48 px link sits on the Home screen without making it scroll', async () => {
			await expect(ana.home.privacyLink).toBeVisible();
			await expect(ana.home.privacyLink).toHaveAttribute('href', `/privacy#${locale}`);
			const box = await ana.home.privacyLink.boundingBox();
			expect(box?.height ?? 0).toBeGreaterThanOrEqual(47.5);
			const scroll = await ana.page.evaluate(
				() => document.documentElement.scrollHeight - window.innerHeight
			);
			expect(scroll, 'the Home screen must still fit one screen').toBeLessThanOrEqual(0);
		});

		await test.step('it is a real page load of the static policy, in the same language', async () => {
			await Promise.all([
				ana.page.waitForURL(`**/privacy#${locale}`),
				ana.home.privacyLink.click()
			]);
			await expect(visibleDoc(ana)).toHaveCount(1);
			await expect(visibleDoc(ana)).toHaveAttribute('id', locale);
			await expect(visibleDoc(ana)).toHaveAttribute('lang', expected.lang);
			await expect(ana.page.getByRole('heading', { level: 1, name: expected.title })).toBeVisible();
			await expect(visibleDoc(ana)).toContainText(expected.sentence);
			await expect(ana.page).toHaveTitle(/Tutti Frutti/);
			// not the SPA: no script at all, and no app shell
			await expect(ana.page.locator('script')).toHaveCount(0);
			await expect(ana.page.locator('main#svelte, [data-sveltekit-preload-data]')).toHaveCount(0);
		});

		await test.step('the page fits the phone screen: no sideways scrolling', async () => {
			const overflow = await ana.page.evaluate(
				() => document.documentElement.scrollWidth - document.documentElement.clientWidth
			);
			expect(overflow).toBeLessThanOrEqual(0);
		});

		await test.step('the contact is there, and every outgoing link goes where the policy says', async () => {
			await expect(
				visibleDoc(ana)
					.getByRole('link', { name: 'aresius' + 'xp@gmail.com' })
					.first()
			).toHaveAttribute('href', 'mailto:aresiusxp@gmail.com');
			const hosts = new Set<string>();
			for (const link of await visibleDoc(ana).locator('a[href^="https://"]').all()) {
				hosts.add(new URL((await link.getAttribute('href')) ?? '').host);
			}
			// Google (sign-in, its policy, permissions), Cloudflare (network and AI judge, its policy), the two data
			// protection authorities, the project's GitHub and the site itself: nothing else
			expect([...hosts].sort()).toEqual([
				'developers.google.com',
				'github.com',
				'myaccount.google.com',
				'policies.google.com',
				'tuttifrutti.aresius.xyz',
				'www.argentina.gob.ar',
				'www.autoriteitpersoonsgegevens.nl',
				'www.cloudflare.com'
			]);
		});

		await test.step('the way back leads to the game', async () => {
			await ana.page.locator('a.back').click();
			await expect(ana.home.nickname).toBeVisible();
			await expect(ana.page).toHaveURL(/\/$/);
		});
	});
}

test('the language bar switches the policy, English is the default, and the bar stays in view', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'es' });
	const bar = ana.page.locator('nav.bar');

	await test.step('without a fragment (or with an unknown one) the policy is in English', async () => {
		for (const url of ['/privacy', '/privacy#zz']) {
			await ana.page.goto(url);
			await expect(visibleDoc(ana)).toHaveCount(1);
			await expect(visibleDoc(ana)).toHaveAttribute('id', 'en');
			await expect(
				ana.page.getByRole('heading', { level: 1, name: POLICY.en.title })
			).toBeVisible();
		}
	});

	await test.step('every language link shows its own language and marks itself as the current one', async () => {
		for (const locale of ['nl', 'es', 'en'] as Locale[]) {
			await bar.getByRole('link', { name: locale.toUpperCase(), exact: true }).click();
			await expect(visibleDoc(ana)).toHaveCount(1);
			await expect(visibleDoc(ana)).toHaveAttribute('id', locale);
			await expect(
				ana.page.getByRole('heading', { level: 1, name: POLICY[locale].title })
			).toBeVisible();
			await expect(visibleDoc(ana)).toContainText(POLICY[locale].sentence);
			// the sticky bar is still on screen after the jump, so the next language is one tap away
			await expect(bar).toBeInViewport();
		}
	});

	await test.step('the same page answers on /privacy/, /privacy.html and its stylesheet', async () => {
		for (const path of ['/privacy/', '/privacy.html']) {
			const response = await ana.page.request.get(path);
			expect(response.status(), path).toBe(200);
			expect(response.headers()['content-type'], path).toMatch(/^text\/html/);
			expect(await response.text(), path).toContain('Privacy Policy');
		}
		const css = await ana.page.request.get('/privacy.css');
		expect(css.status()).toBe(200);
		expect(css.headers()['content-type']).toMatch(/^text\/css/);
	});
});

test('the policy is served fresh and with the same security headers as the app', async ({
	app
}) => {
	const ana = await app.player('Ana');
	for (const path of ['/privacy', '/privacy.css']) {
		const headers = (await ana.page.request.get(path)).headers();
		expect(headers['cache-control'], path).toBe('no-cache');
		expect(headers['content-security-policy'], path).toContain("default-src 'self'");
		expect(headers['content-security-policy'], path).toContain("frame-ancestors 'none'");
		expect(headers['x-content-type-options'], path).toBe('nosniff');
	}
});

test('an app that already has its service worker still shows the policy, not the app shell', async ({
	app
}) => {
	const ana = await app.player('Ana', { locale: 'en' });
	await ana.home.open();
	await expect
		.poll(() =>
			ana.page.evaluate(
				async () => (await navigator.serviceWorker.getRegistration())?.active?.state
			)
		)
		.toBe('activated');
	await ana.page.reload();
	await expect
		.poll(() => ana.page.evaluate(() => navigator.serviceWorker.controller !== null))
		.toBe(true);

	// a page controlled by the worker: the navigation must go to the network, not get index.html
	await ana.home.privacyLink.click();
	await expect(ana.page.getByRole('heading', { level: 1, name: POLICY.en.title })).toBeVisible();
	await expect(ana.home.nickname).toHaveCount(0);
	await ana.page.goto('/privacy.html#nl');
	await expect(ana.page.getByRole('heading', { level: 1, name: POLICY.nl.title })).toBeVisible();
});

test('the policy names the cookies the app really sets', async ({ app }) => {
	const ana = await app.player('Ana', { locale: 'en' });
	await ana.home.open();
	await ana.home.setNickname('Ana');
	await createRoom(app, ana);

	const cookies = await ana.context.cookies();
	expect(cookies.map((c) => c.name).sort(), 'a guest has exactly one cookie').toEqual(['tf_guest']);
	const guest = cookies[0]!;
	expect(guest.httpOnly).toBe(true);
	expect(guest.sameSite).toBe('Lax');
	// about one year, as the policy says
	const days = (guest.expires - Date.now() / 1000) / 86400;
	expect(days).toBeGreaterThan(360);
	expect(days).toBeLessThanOrEqual(366);

	await ana.page.goto('/privacy#en');
	for (const cookie of cookies) {
		await expect(visibleDoc(ana)).toContainText(cookie.name);
	}
	// the browser storage the policy lists is what the app really keeps on the device
	const storage = await ana.page.evaluate(() => Object.keys(localStorage).sort());
	expect(storage.filter((key) => key.startsWith('tf.'))).toEqual(
		expect.arrayContaining(['tf.avatar', 'tf.nickname'])
	);
});

for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`no serious accessibility violations on the policy (${locale})`, async ({ app }) => {
		const ana = await app.player('Ana', { locale, reducedMotion: true });
		await ana.page.goto(`/privacy#${locale}`);
		await expect(visibleDoc(ana)).toHaveAttribute('id', locale);
		await expectAccessible(ana, 'privacy policy');
	});
}
