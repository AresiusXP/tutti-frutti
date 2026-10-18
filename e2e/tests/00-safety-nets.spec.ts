import { expectAccessible } from '../src/a11y';
import { expectEnglishOnly } from '../src/english-only';
import { expect, test } from '../src/fixtures';

/**
 * The safety nets of the suite must catch what they are there for, or every green run means less.
 * Each test provokes the problem on purpose, checks it was reported and then clears the report.
 */
test.describe('the safety nets', () => {
	test.describe.configure({ mode: 'parallel' });

	test('the guard reports page errors, console errors, failed requests and CSP violations', async ({
		app
	}) => {
		const ana = await app.player('Ana');
		await ana.home.open();
		const problems = () => app.guard.problems.join('\n');

		await ana.page.evaluate(() =>
			setTimeout(() => {
				throw new Error('boom');
			}, 0)
		);
		await expect.poll(problems).toContain('pageerror: boom');

		await ana.page.evaluate(() => console.error('kaboom'));
		await expect.poll(problems).toContain('console.error: kaboom');

		await ana.page.evaluate(() => fetch('/api/definitely-not-there'));
		await expect.poll(problems).toMatch(/HTTP 404: GET .*\/api\/definitely-not-there/);

		await ana.page.route('**/api/blocked', (route) => route.abort('connectionreset'));
		await ana.page.evaluate(() => fetch('/api/blocked').catch(() => undefined));
		await expect.poll(problems).toMatch(/request failed: GET .*\/api\/blocked/);

		// a CSP report cannot be allowed away, whatever the allowances say
		app.guard.allow({ kind: 'console', text: /./, why: 'self-test' });
		await ana.page.evaluate(() =>
			console.warn(
				"Refused to load the script 'https://x.test/a.js' because it violates the following Content Security Policy directive: \"script-src 'self'\"."
			)
		);
		await expect.poll(problems).toContain('CSP violation');

		app.guard.problems.length = 0;
	});

	test('an allowance silences exactly what it names', async ({ app }) => {
		const ana = await app.player('Ana');
		await ana.home.open();
		app.guard.allow({ kind: 'response', url: /\/api\/expected$/, status: 404, why: 'self-test' });
		await ana.page.evaluate(() => fetch('/api/expected'));
		await ana.page.evaluate(() => fetch('/api/unexpected'));
		await expect.poll(() => app.guard.problems.join('\n')).toContain('/api/unexpected');
		expect(app.guard.problems.filter((p) => p.includes('/api/expected'))).toEqual([]);
		app.guard.problems.length = 0;
	});

	test('the English-only check fails on a Spanish screen', async ({ app }) => {
		const ana = await app.player('Ana', { locale: 'es' });
		await ana.home.open();
		await expect(expectEnglishOnly([ana], 'home')).rejects.toThrow();
		const ben = await app.player('Ben', { locale: 'en' });
		await ben.home.open();
		await expectEnglishOnly([ben], 'home');
	});

	test('the accessibility check fails on a serious violation', async ({ app }) => {
		const ana = await app.player('Ana');
		await ana.home.open();
		await expectAccessible(ana, 'home');
		await ana.page.evaluate(() =>
			document.body.insertAdjacentHTML('beforeend', '<button type="button"></button>')
		);
		await expect(expectAccessible(ana, 'home with a nameless button')).rejects.toThrow(
			/button-name/
		);
	});
});
