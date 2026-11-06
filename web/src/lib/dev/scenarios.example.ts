import { FakeSession } from '#lib/game/fake.svelte.ts';
import { makeView } from '#lib/game/fixtures.ts';
import type { Scenario } from './scenarios';

export const scenarios: Record<string, Scenario> = {
	'example-lobby-host': () => new FakeSession(makeView())
};
