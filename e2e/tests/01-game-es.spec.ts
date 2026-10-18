import { test } from '../src/fixtures';
import { playReferenceGame } from '../src/scenarios/reference-game';

test('a full three-player game in Spanish, from the lobby to the rematch', async ({ app }) => {
	await playReferenceGame(app, 'es');
});
