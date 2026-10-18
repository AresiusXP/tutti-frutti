import { expectAccessible, KNOWN_VIOLATIONS } from '../src/a11y';
import { answersLeaving, askToStop, openRound } from '../src/flows';
import { expectUnobscured } from '../src/layout';
import { expectDutchOnly, expectEnglishOnly } from '../src/no-spanish';
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

	test('the Dutch check fails on a Spanish screen and passes on a Dutch one', async ({ app }) => {
		const ana = await app.player('Ana', { locale: 'es' });
		await ana.home.open();
		await expect(expectDutchOnly([ana], 'home')).rejects.toThrow();

		const daan = await app.player('Daan', { locale: 'nl' });
		await daan.home.open();
		await expectDutchOnly([daan], 'home');

		// Dutch spells ë, é, ï: only what Dutch does not have is Spanish
		await daan.page.evaluate(() =>
			document.body.insertAdjacentHTML(
				'beforeend',
				'<p>Categorieën, één café, reünie, coöperatie</p>'
			)
		);
		await expectDutchOnly([daan], 'home with Dutch accents');

		// each kind of leak is caught by itself (the language of the page is still right)
		for (const leak of ['¿Dónde está?', 'Mañana', '¡Hola!', 'Basta ya']) {
			await daan.page.evaluate(
				(text) => document.body.insertAdjacentHTML('beforeend', `<p id="leak">${text}</p>`),
				leak
			);
			await expect(expectDutchOnly([daan], 'home with a leak'), leak).rejects.toThrow(
				/Spanish text on Daan's home with a leak screen/
			);
			await daan.page.evaluate(() => document.getElementById('leak')?.remove());
		}
		await daan.page.evaluate(() => document.documentElement.setAttribute('lang', 'es'));
		await expect(expectDutchOnly([daan], 'home with the wrong lang')).rejects.toThrow(/lang/);
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

	test('the accessibility check also fails on the two moderate rules it enforces, and only on those', async ({
		app
	}) => {
		const ana = await app.player('Ana');
		await ana.home.open();
		await expectAccessible(ana, 'home');

		// label in name (WCAG 2.5.3): a button that shows "60 s" but is named "60 seconds"
		await ana.page.evaluate(() =>
			document.body.insertAdjacentHTML(
				'beforeend',
				'<button type="button" id="mismatch" aria-label="60 seconds">60 s</button>'
			)
		);
		await expect(expectAccessible(ana, 'home with a mismatched name')).rejects.toThrow(
			/label-content-name-mismatch/
		);
		await ana.page.evaluate(() => document.getElementById('mismatch')?.remove());

		// another moderate finding (a skipped heading level) is reported, not a failure
		await ana.page.evaluate(() =>
			document.body.insertAdjacentHTML('beforeend', '<h4 id="skipped">Skipped levels</h4>')
		);
		await expectAccessible(ana, 'home with a skipped heading level');
		expect(
			test
				.info()
				.annotations.filter(
					(a) => a.type === 'a11y-minor' && /heading-order/.test(a.description ?? '')
				),
			'the skipped level is reported'
		).not.toHaveLength(0);
		await ana.page.evaluate(() => document.getElementById('skipped')?.remove());

		// a screen with two h1s: axe is content with one or more, the design promises exactly one
		await ana.page.evaluate(() =>
			document.body.insertAdjacentHTML('beforeend', '<h1 id="second">A second title</h1>')
		);
		await expect(expectAccessible(ana, 'home with two h1s')).rejects.toThrow(/exactly one h1/);
		// an h1 nobody can perceive (display: none) does not count as a second one
		await ana.page.evaluate(() => {
			const second = document.getElementById('second');
			if (second) second.style.display = 'none';
		});
		await expectAccessible(ana, 'home with a hidden second h1');
		await ana.page.evaluate(() => document.getElementById('second')?.remove());

		// a screen without an h1
		await ana.page.evaluate(() => document.querySelectorAll('h1').forEach((h) => h.remove()));
		await expect(expectAccessible(ana, 'home without an h1')).rejects.toThrow(
			/page-has-heading-one/
		);
	});

	test('an allowance in KNOWN_VIOLATIONS silences exactly the element it names, nothing else', async ({
		app
	}) => {
		const ana = await app.player('Ana');
		await ana.home.open();
		await ana.page.evaluate(() =>
			document.body.insertAdjacentHTML(
				'beforeend',
				'<button type="button" id="tolerated"></button><button type="button" id="other"></button>'
			)
		);
		expect(KNOWN_VIOLATIONS, 'the list starts empty').toEqual([]);
		KNOWN_VIOLATIONS.push({
			rule: 'button-name',
			target: /^#tolerated$/,
			why: 'safety-net test: this one is allowed'
		});
		try {
			// the other nameless button still fails, and the message names it, not the tolerated one
			const failure = expectAccessible(ana, 'home with two nameless buttons');
			await expect(failure).rejects.toThrow(/button-name/);
			await expect(failure).rejects.toThrow(/#other/);
			await expect(failure).rejects.not.toThrow(/#tolerated/);
			await ana.page.evaluate(() => document.getElementById('other')?.remove());
			await expectAccessible(ana, 'home with only the tolerated button');
		} finally {
			KNOWN_VIOLATIONS.length = 0;
		}
	});

	test('the vote-to-stop checks fail when the vote, the prompt, the dialog or the frame is absent', async ({
		app
	}) => {
		const {
			players: [ana, ben],
			letter
		} = await openRound(app, { names: ['Ana', 'Ben'] });
		if (!ana || !ben) throw new Error('two players expected');
		await ana.playing.fillAll(answersLeaving(letter, ana, [5]));
		const quick = { timeout: 700 };

		// no vote yet: the "absent" checks pass, every "present" check fails
		await ana.playing.expectNoVote();
		await ben.playing.expectNoVote();
		await expect(ana.playing.expectTally(0, 1, quick)).rejects.toThrow(/stop-wait-banner/);
		await expect(ben.playing.voteTitle('Ana')).toBeHidden(quick);
		await expect(ana.playing.askDialog).toBeHidden(quick);
		await expect(ana.playing.hint(ana.t('play_hint_ready'))).toBeHidden(quick);
		expect(ana.sentCount('ask_stop')).toBe(0);

		// a vote is open: the "no vote" check fails on the asker's banner and on the voter's prompt,
		// and the tally, the asker's name, the waiting line and the frame count are exact
		await askToStop(ana, [ben]);
		await expect(ana.playing.expectNoVote(quick)).rejects.toThrow(/stop-wait-banner/);
		await expect(ben.playing.expectNoVote(quick)).rejects.toThrow(/stop-vote-sheet/);
		await ana.playing.expectTally(0, 1);
		await expect(ana.playing.expectTally(1, 1, quick)).rejects.toThrow(/dijeron que sí/);
		await expect(ana.playing.expectTally(0, 2, quick)).rejects.toThrow(/dijeron que sí/);
		await expect(ben.playing.voteTitle('Cleo')).toBeHidden(quick);
		await expect(ben.playing.voteWaiting(1, 1)).toBeHidden(quick);
		await expect(ben.playing.onlooker('Ana')).toBeHidden(quick);
		expect(ana.sentCount('ask_stop')).toBe(1);
		expect(ben.sentCount('ask_stop')).toBe(0);
		expect(ben.sentCount('vote_stop')).toBe(0);

		// the visibility check sees a cover and a field pushed off the screen
		const field = ben.playing.field('Nombre');
		await expectUnobscured(field, 'the first field', quick);
		await ben.page.evaluate(() => {
			// styled through the CSSOM: the page's CSP refuses inline `style` attributes (and the guard says so)
			const cover = document.createElement('div');
			cover.id = 'cover';
			Object.assign(cover.style, { position: 'fixed', inset: '0', zIndex: '99' });
			document.body.append(cover);
		});
		await expect(expectUnobscured(field, 'the first field', quick)).rejects.toThrow(/lies over/);
		await ben.page.evaluate(() => document.getElementById('cover')?.remove());
		await expectUnobscured(field, 'the first field', quick);
		await field.evaluate((el) => (el.style.transform = 'translateY(3000px)'));
		await expect(expectUnobscured(field, 'the first field', quick)).rejects.toThrow(
			/below the screen/
		);
	});
});
