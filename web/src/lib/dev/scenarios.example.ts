import { FakeSession } from '$lib/game/fake.svelte';
import { makeView } from '$lib/game/fixtures';
import type { Scenario } from './scenarios';

export const scenarios: Record<string, Scenario> = {
	'example-lobby-host': () => new FakeSession(makeView())
};
