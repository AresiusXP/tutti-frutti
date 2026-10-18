/**
 * jsdom stand-ins for the axe rules the e2e suite enforces on the real app, so that a regression
 * shows up in `npm run test` first:
 *  - `page-has-heading-one`: every screen has exactly one `<h1>`;
 *  - `label-content-name-mismatch` (WCAG 2.5.3, label in name): a control whose accessible name was
 *    overridden (`aria-label`) must still contain the text it shows, or a voice-control user who
 *    says what they SEE cannot reach it. Like axe, the visible text must match whole words ("60 s" is
 *    not inside "60 segundos");
 *  - the form-field attributes Chrome's DevTools "Issues" panel asks for (`id` or `name`, an
 *    `autocomplete` value).
 */

/** What axe compares: lower case, no emoji, symbols, punctuation or runs of spaces. */
export function curate(text: string): string {
	return text
		.normalize('NFKC')
		.toLowerCase()
		.replace(/[\p{Extended_Pictographic}\p{S}\p{P}\s]+/gu, ' ')
		.trim();
}

const WIDGETS =
	'button, a[href], input, select, textarea, [role="button"], [role="switch"], [role="link"], [role="tab"], [role="checkbox"], [role="radio"], [role="menuitem"]';

/** The controls that show some text but carry an `aria-label` that does not contain it. */
export function labelNameMismatches(root: ParentNode): string[] {
	const found: string[] = [];
	for (const el of root.querySelectorAll<HTMLElement>(WIDGETS)) {
		const label = el.getAttribute('aria-label');
		if (label === null) continue;
		const visible = curate(el.textContent ?? '');
		if (visible === '' || ` ${curate(label)} `.includes(` ${visible} `)) continue;
		found.push(`"${(el.textContent ?? '').replace(/\s+/g, ' ').trim()}" is named "${label}"`);
	}
	return found;
}

/** The `<h1>`s a screen shows (ignoring the ones inside `aria-hidden`). */
export function headingOnes(root: ParentNode): string[] {
	return [...root.querySelectorAll<HTMLElement>('h1')]
		.filter((h) => !h.closest('[aria-hidden="true"]'))
		.map((h) => (h.textContent ?? '').replace(/\s+/g, ' ').trim());
}

/** Text fields without an `id`/`name` or without an `autocomplete` value. */
export function fieldProblems(root: ParentNode): string[] {
	const found: string[] = [];
	const seen = new Set<string>();
	for (const el of root.querySelectorAll<HTMLInputElement>('input, textarea, select')) {
		if (el instanceof HTMLInputElement && ['button', 'submit', 'hidden'].includes(el.type))
			continue;
		const where = `<${el.localName} type=${el.type ?? ''} id=${el.id} name=${el.name}>`;
		if (!el.id) found.push(`${where} has no id`);
		if (!el.name) found.push(`${where} has no name`);
		if (el.id) {
			if (seen.has(el.id)) found.push(`${where} repeats an id`);
			seen.add(el.id);
		}
		if (!el.getAttribute('autocomplete')) found.push(`${where} has no autocomplete`);
	}
	return found;
}
