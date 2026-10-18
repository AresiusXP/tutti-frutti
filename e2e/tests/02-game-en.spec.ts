import { test } from '../src/fixtures';
import { expectEnglishOnly } from '../src/english-only';
import { playReferenceGame } from '../src/scenarios/reference-game';

test('the same game in English says "Stop!" and shows no Spanish anywhere', async ({ app }) => {
	await playReferenceGame(app, 'en', {
		onScreen: (players, phase) => expectEnglishOnly(players, phase)
	});
});
