import { expect, test } from '../src/fixtures';
import { ORIGIN } from '../src/env';

interface Manifest {
	name?: string;
	short_name?: string;
	start_url?: string;
	display?: string;
	icons?: { src: string; sizes?: string; type?: string; purpose?: string }[];
}

test('the service worker registers on the first visit and the app shell loads offline', async ({
	app
}) => {
	const ana = await app.player('Ana', { network: true });
	const page = ana.page;
	const registration = () =>
		page.evaluate(async () => {
			const reg = await navigator.serviceWorker.getRegistration();
			return reg?.active?.state ?? null;
		});

	await test.step('first visit: a service worker is registered and activates (precache done)', async () => {
		await ana.home.open();
		await ana.home.setNickname('Ana');
		await expect.poll(registration, { message: 'service worker state' }).toBe('activated');
	});

	await test.step('a fresh load shows no "new version" prompt', async () => {
		await expect(page.getByText(ana.t('update_available'), { exact: true })).toHaveCount(0);
		await page.reload();
		await expect(ana.home.nickname).toBeVisible();
		await expect
			.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null))
			.toBe(true);
		await expect(page.getByText(ana.t('update_available'), { exact: true })).toHaveCount(0);
		await expect(ana.offlineBanner).toHaveCount(0);
	});

	await test.step('offline: reloading "/" still shows the app, with the offline banner', async () => {
		await ana.network?.drop();
		await page.reload();
		await expect(ana.home.nickname).toHaveValue('Ana'); // the profile is local: it is still there
		await expect(ana.home.createButton).toBeEnabled();
		await expect(page.getByText(ana.t('tagline'), { exact: true })).toBeVisible();
		await expect(ana.offlineBanner).toBeVisible();
	});

	await test.step('back online: the banner goes away by itself', async () => {
		await ana.network?.restore();
		await expect(ana.offlineBanner).toHaveCount(0);
		await expect(ana.home.createButton).toBeEnabled();
	});
});

test('the web manifest makes the app installable', async ({ app, request }) => {
	const ana = await app.player('Ana');
	await ana.home.open();
	const link = ana.page.locator('link[rel="manifest"]');
	await expect(link, 'the page links its manifest').toHaveAttribute('href', /\S/);
	const href = (await link.getAttribute('href')) ?? '';

	const response = await request.get(new URL(href, ORIGIN).toString());
	expect(response.ok()).toBe(true);
	const manifest = (await response.json()) as Manifest;
	expect(manifest.name).toBe('Tutti Frutti');
	expect(manifest.short_name).toBe('Tutti Frutti');
	expect(manifest.display).toMatch(/standalone|fullscreen|minimal-ui/);
	expect(manifest.start_url).toBeTruthy();

	const sizes = (manifest.icons ?? []).map((icon) => icon.sizes);
	expect(sizes, 'a 192 px and a 512 px icon').toEqual(
		expect.arrayContaining(['192x192', '512x512'])
	);
	expect((manifest.icons ?? []).some((icon) => /maskable/.test(icon.purpose ?? ''))).toBe(true);
	for (const icon of manifest.icons ?? []) {
		const image = await request.get(new URL(icon.src, ORIGIN).toString());
		expect(image.ok(), `icon ${icon.src}`).toBe(true);
		expect(image.headers()['content-type']).toMatch(/^image\//);
	}
});

test('link-preview crawlers (no JavaScript) get the Open Graph card on Home and on invite links', async ({
	request
}) => {
	const meta = (html: string, property: string) =>
		new RegExp(`<meta\\s+property="${property}"\\s+content="([^"]*)"`).exec(html)?.[1];

	for (const path of ['/', '/KT7P']) {
		const response = await request.get(new URL(path, ORIGIN).toString(), {
			headers: { 'user-agent': 'Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)' }
		});
		expect(response.ok(), path).toBe(true);
		const html = await response.text();
		expect(meta(html, 'og:title'), path).toBe('Tutti Frutti');
		expect(meta(html, 'og:description')?.length, path).toBeGreaterThan(40);
		expect(html, path).toMatch(/<meta\s+name="twitter:card"\s+content="summary_large_image"/);

		// The URL names the production host (crawlers need an absolute one); on the stack under test
		// only the path is fetched.
		const image = new URL(meta(html, 'og:image') ?? '');
		expect(image.protocol, path).toBe('https:');
		const picture = await request.get(new URL(image.pathname, ORIGIN).toString());
		expect(picture.ok(), `${path}: ${image.pathname}`).toBe(true);
		expect(picture.headers()['content-type'], image.pathname).toBe('image/png');
		const body = await picture.body();
		expect(body.subarray(1, 4).toString('latin1'), 'a real PNG, not the SPA fallback').toBe('PNG');
	}
});
