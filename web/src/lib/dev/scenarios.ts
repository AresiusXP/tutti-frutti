/**
 * Dev playground registry (`/dev`, only in `vite dev`). Every
 * `scenarios.<area>.ts` file in this folder exports `scenarios`, a map from a
 * kebab-case name ("lobby-host", "playing-basta-grace") to a factory that
 * returns a ready `FakeSession`. Add scenarios in YOUR OWN file so parallel work
 * never collides; they are picked up automatically.
 */
import type { FakeSession } from '$lib/game/fake.svelte';

export type Scenario = () => FakeSession;

const modules = import.meta.glob<{ scenarios: Record<string, Scenario> }>('./scenarios.*.ts', {
	eager: true
});

export const scenarios: Record<string, Scenario> = Object.assign(
	{},
	...Object.values(modules).map((module) => module.scenarios)
);
