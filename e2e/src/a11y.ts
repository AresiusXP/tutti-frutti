import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import type { Player } from './player';

/**
 * Findings that are reported but not fixed yet: each is a TODO for the web app, with why it is
 * tolerated here. Matched per node (rule + the element's selector), so a NEW offender on the same
 * screen still fails. Keep the list short; delete an entry the day its fix lands.
 */
interface Known {
	rule: string;
	target: RegExp;
	why: string;
}

export const KNOWN_VIOLATIONS: Known[] = [
	{
		rule: 'label-content-name-mismatch',
		target: /^\.min-h-20$/,
		why: 'TODO(web, lobby RoomHeader): the code button shows "KT7P" but is named "Copiar el código K T 7 P" (spelled out for screen readers); WCAG 2.5.3 wants the visible text inside the name, voice control users cannot say "KT7P". Fix: name it "KT7P. Copiar el código" or set aria-label to code + action.'
	},
	{
		rule: 'label-content-name-mismatch',
		target:
			/^button\[aria-label="(No time limit|Sin límite de tiempo|Geen tijdslimiet|\d+ (seconds|segundos|seconden))"\]$/,
		why: 'TODO(web, lobby Segmented): the time-limit buttons show "60 s" / "Libre" (Dutch: "Uit") but are named "60 segundos" / "Sin límite de tiempo" (Dutch: "60 seconden" / "Geen tijdslimiet"; WCAG 2.5.3). Fix: put the visible text in the name ("60 s", "Libre — sin límite de tiempo") or drop the aria-label.'
	},
	{
		rule: 'label-content-name-mismatch',
		target: /^\.mt-2\.gap-2 > (button|\.rounded-xl)/,
		why: 'TODO(web, review AnswerCard): the reject button shows "Rechazar 0/2" but is named "Rechazar la respuesta de Beto: 0 de 2 votos" (WCAG 2.5.3: "0/2" is not in the name). Fix: write the tally the same way in both ("0/2 votos") or add the visible tally verbatim to the name.'
	}
];

/** Axe on the player's current screen: no `serious` or `critical` violation outside KNOWN_VIOLATIONS. */
export async function expectAccessible(player: Player, screen: string): Promise<void> {
	const results = await new AxeBuilder({ page: player.page }).analyze();
	const serious: string[] = [];
	for (const violation of results.violations) {
		if (violation.impact !== 'serious' && violation.impact !== 'critical') {
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
		serious.push(
			`${violation.id} [${violation.impact}] ${violation.help}: ${unknown
				.slice(0, 4)
				.map((n) => n.target.join(' '))
				.join(' | ')}`
		);
	}
	expect(
		serious,
		`serious/critical accessibility violations on ${player.name}'s ${screen} screen (${player.locale})`
	).toEqual([]);
}
