import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import type { Player } from './player';

/**
 * Findings that are reported but not fixed yet: each is a TODO for the web app, with why it is
 * tolerated here. Matched per node (rule + the element's selector), so a NEW offender on the same
 * screen still fails. Keep the list short (it is empty today: leave it that way); delete an entry the
 * day its fix lands.
 */
interface Known {
	rule: string;
	target: RegExp;
	why: string;
}

export const KNOWN_VIOLATIONS: Known[] = [];

/**
 * Rules that fail the check whatever their impact. Axe rates both as `moderate`, but each is a
 * WCAG-level promise the app keeps on every screen:
 *  - `page-has-heading-one`: every screen has one `h1` (a visually hidden one where the design shows none);
 *  - `label-content-name-mismatch` (WCAG 2.5.3, label in name): a control's accessible name contains the
 *    text it shows, so that someone who says "60 s" or "Rechazar 0/2" to a voice-control tool reaches it.
 * Every other `moderate` / `minor` finding is only reported.
 */
export const ALWAYS_BLOCKING = ['page-has-heading-one', 'label-content-name-mismatch'];

/**
 * Axe's `page-has-heading-one` only asks for at least one `h1`; the design promises exactly one
 * (a second one muddies the heading outline a screen reader walks). Counted in the accessibility tree,
 * so a visually hidden `h1` counts and a `display: none` / `aria-hidden` one does not.
 */
async function expectOneHeadingOne(player: Player, screen: string): Promise<void> {
	const titles = await player.page.getByRole('heading', { level: 1 }).allTextContents();
	expect(
		titles.map((t) => t.trim()),
		`exactly one h1 on ${player.name}'s ${screen} screen (${player.locale})`
	).toHaveLength(1);
}

const isBlocking = (violation: { id: string; impact?: string | null }) =>
	violation.impact === 'serious' ||
	violation.impact === 'critical' ||
	ALWAYS_BLOCKING.includes(violation.id);

/**
 * The player's current screen has exactly one `h1`, and axe finds no `serious` or `critical`
 * violation and none of the ALWAYS_BLOCKING rules outside KNOWN_VIOLATIONS.
 */
export async function expectAccessible(player: Player, screen: string): Promise<void> {
	const results = await new AxeBuilder({ page: player.page }).analyze();
	const blocking: string[] = [];
	for (const violation of results.violations) {
		if (!isBlocking(violation)) {
			// not a failure; visible in the HTML report (and in the console with E2E_A11Y_VERBOSE=1)
			const note = `${violation.id} [${violation.impact}] on ${player.name}'s ${screen}: ${violation.nodes.length} node(s)`;
			test.info().annotations.push({ type: 'a11y-minor', description: note });
			if (process.env.E2E_A11Y_VERBOSE) console.log(note);
			continue;
		}
		const unknown = violation.nodes.filter(
			(node) =>
				!KNOWN_VIOLATIONS.some(
					(k) => k.rule === violation.id && k.target.test(node.target.join(' '))
				)
		);
		if (unknown.length === 0) continue;
		blocking.push(
			`${violation.id} [${violation.impact}] ${violation.help}: ${unknown
				.slice(0, 4)
				.map((n) => n.target.join(' '))
				.join(' | ')}`
		);
	}
	expect(
		blocking,
		`accessibility violations on ${player.name}'s ${screen} screen (${player.locale})`
	).toEqual([]);
	// after axe, so a screen without any h1 is reported by the named rule first
	await expectOneHeadingOne(player, screen);
}
