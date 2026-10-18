// @vitest-environment jsdom
import { cleanup, render } from '@testing-library/svelte';
import { flushSync } from 'svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { scenarios } from '$lib/dev/scenarios';
import { i18n } from '$lib/i18n.svelte';
import RoomScreen from '$lib/screens/RoomScreen.svelte';
import { fieldProblems, headingOnes, labelNameMismatches } from './a11y.testkit';

/**
 * Every dev-playground scenario (every phase, every state the playground can stage) rendered in
 * each screen language: one `<h1>`, a name that contains the visible text, well-formed fields.
 * The e2e suite runs axe on the real app; this is the fast, early version of the same rules.
 */
const names = Object.keys(scenarios).sort();

describe('every playground scenario', () => {
	beforeEach(() => i18n.set('es'));
	afterEach(() => cleanup());

	it('has scenarios to look at', () => {
		expect(names.length).toBeGreaterThan(100);
	});

	for (const locale of ['es', 'en', 'nl'] as const) {
		describe(`in ${locale}`, () => {
			it('shows exactly one h1, names controls after what they show and has well-formed fields', () => {
				const problems: string[] = [];
				for (const name of names) {
					i18n.set(locale);
					const session = scenarios[name]!();
					const { container } = render(RoomScreen, { session });
					if (session.devTap) {
						const target = document.querySelector<HTMLElement>(`[data-testid="${session.devTap}"]`);
						if (target) {
							target.click();
							// the staged state (a confirm sheet, a vote…) must be on screen before it is checked
							flushSync();
						} else {
							problems.push(`${name}: the staged tap "${session.devTap}" has no target on screen`);
						}
					}
					const h1 = headingOnes(document.body);
					if (h1.length !== 1) problems.push(`${name}: ${h1.length} h1 (${h1.join(' | ')})`);
					for (const mismatch of labelNameMismatches(document.body)) {
						problems.push(`${name}: ${mismatch}`);
					}
					for (const problem of fieldProblems(document.body)) problems.push(`${name}: ${problem}`);
					expect(container).toBeTruthy();
					cleanup();
				}
				expect(problems).toEqual([]);
			}, 60_000);
		});
	}
});
