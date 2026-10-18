import { expect, type Locator } from '@playwright/test';

/**
 * What a phone user needs from a control: all of it inside the screen, and nothing lying over it (a sticky
 * header or footer, a banner, an overlay). Each problem found is one readable line; none = fine.
 * Hit-testing at five points that are inside the box even with rounded corners.
 */
function problems(el: Element): string[] {
	const box = el.getBoundingClientRect();
	const width = window.innerWidth;
	const height = window.innerHeight;
	const found: string[] = [];
	const at = `(${Math.round(box.left)},${Math.round(box.top)} ${Math.round(box.width)}x${Math.round(box.height)} in ${width}x${height})`;
	if (box.width === 0 || box.height === 0) found.push(`it has no size ${at}`);
	if (box.top < 0) found.push(`it starts above the screen ${at}`);
	if (box.bottom > height) found.push(`it ends below the screen ${at}`);
	if (box.left < 0 || box.right > width) found.push(`it sticks out sideways ${at}`);
	const points: [string, number, number][] = [
		['top edge', box.left + box.width / 2, box.top + 2],
		['bottom edge', box.left + box.width / 2, box.bottom - 2],
		['left edge', box.left + 20, box.top + box.height / 2],
		['right edge', box.right - 20, box.top + box.height / 2],
		['centre', box.left + box.width / 2, box.top + box.height / 2]
	];
	for (const [where, x, y] of points) {
		if (x < 0 || y < 0 || x > width || y > height) continue; // already reported above
		const hit = document.elementFromPoint(x, y);
		if (hit === el || (hit && el.contains(hit))) continue;
		const what = hit
			? `${hit.tagName.toLowerCase()}${hit.getAttribute('data-testid') ? `[${hit.getAttribute('data-testid')}]` : ''}`
			: 'nothing';
		found.push(`${what} lies over its ${where}`);
	}
	return found;
}

/** The element is wholly on screen and not covered by anything: auto-retried (a scroll may still be settling). */
export async function expectUnobscured(
	locator: Locator,
	what: string,
	options: { timeout?: number } = {}
): Promise<void> {
	await expect
		.poll(() => locator.evaluate(problems), { message: `${what} is fully visible`, ...options })
		.toEqual([]);
}
