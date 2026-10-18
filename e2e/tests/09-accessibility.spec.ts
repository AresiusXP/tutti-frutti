import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import { bastaBy, chooserStops, createRoom, startGame, uniqueAnswers } from '../src/flows';
import type { Locale } from '../src/messages';

test.describe.configure({ mode: 'parallel' });

/**
 * A small smoke, not an audit: axe on the screens a player spends the game on, in each screen language.
 * Reduced motion so entrance animations cannot skew the contrast checks.
 */
for (const locale of ['es', 'en', 'nl'] as Locale[]) {
	test(`no serious accessibility violations on the main screens (${locale})`, async ({ app }) => {
		const ana = await app.player('Ana', { locale, reducedMotion: true });
		const beto = await app.player('Beto', { locale, reducedMotion: true });
		const everyone = [ana, beto];

		await ana.home.open();
		await ana.home.setNickname('Ana');
		await expectAccessible(ana, 'home');

		const code = await createRoom(app, ana);
		await beto.page.goto(`/${code}`);
		await beto.gate.expectOpen(code);
		await expectAccessible(beto, 'gate');
		await beto.gate.setNickname('Beto');
		await beto.gate.join();
		await beto.lobby.expectVisible(code);
		await ana.lobby.expectPlayers(['Ana', 'Beto']);
		await expectAccessible(ana, 'lobby (host)');
		await expectAccessible(beto, 'lobby (guest)');

		await ana.picker.open();
		await expectAccessible(ana, 'category picker');
		await ana.picker.done();

		await startGame(ana, everyone);
		await expectAccessible(ana, 'letter (chooser)');
		await expectAccessible(beto, 'letter (waiting)');
		await chooserStops(everyone);
		const { letter } = await ana.playing.roundInfo();
		await ana.playing.fillAll(uniqueAnswers(letter, ana, 6));
		// Beto: four good answers, one empty field and one that does not start with the letter
		const wrong = letter === 'A' ? 'Bxx' : 'Axx';
		await beto.playing.fillAll([...uniqueAnswers(letter, beto, 4), null, wrong]);
		await expect(ana.playing.progress('Beto', 5, 6)).toBeAttached();
		await expectAccessible(ana, 'playing');
		await expectAccessible(beto, 'playing (empty and wrong-letter fields)');

		await bastaBy(ana, [beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));
		await expectAccessible(ana, 'review (host)');
		await expectAccessible(beto, 'review (guest)');
		await ana.review.finish({ anyway: true });

		await ana.expectScreen('summary');
		await expectAccessible(ana, 'summary');
		await ana.summary.endGame();
		await ana.expectScreen('final');
		await expectAccessible(ana, 'final (host)');
		await beto.expectScreen('final');
		await expectAccessible(beto, 'final (guest)');
	});
}
