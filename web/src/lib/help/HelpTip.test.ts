// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { i18n } from '#lib/i18n.svelte.ts';
import HelpTip from './HelpTip.svelte';
import { tips } from './tips.svelte.ts';

beforeEach(() => {
	i18n.set('en');
	tips.reset();
});
afterEach(() => {
	cleanup();
	tips.reset();
	localStorage.setItem('tf.tips', JSON.stringify(['playing', 'review']));
});

describe('HelpTip', () => {
	it('is a labelled card with the text and two 48 px answers', () => {
		render(HelpTip, { id: 'review', text: 'Tap Reject.' });
		const card = screen.getByRole('region', { name: 'Tip' });
		expect(card.textContent).toContain('Tap Reject.');
		for (const name of ['Got it', 'How to play']) {
			expect(screen.getByRole('button', { name }).className).toContain('h-12');
		}
	});

	it('"Got it" counts the tip as seen', async () => {
		render(HelpTip, { id: 'review', text: 'x' });
		expect(tips.seen('review')).toBe(false);
		await fireEvent.click(screen.getByRole('button', { name: 'Got it' }));
		expect(tips.seen('review')).toBe(true);
		expect(tips.seen('playing')).toBe(false);
	});

	it('leaving the screen counts as seen too, so it does not come back every round', () => {
		const { unmount } = render(HelpTip, { id: 'playing', text: 'x' });
		expect(tips.seen('playing')).toBe(false);
		unmount();
		expect(tips.seen('playing')).toBe(true);
	});

	it('"How to play" opens the guide at the part of the tip and leaves the tip in place', async () => {
		render(HelpTip, { id: 'review', text: 'x' });
		await fireEvent.click(screen.getByRole('button', { name: 'How to play' }));
		const dialog = screen.getByRole('dialog', { name: 'How to play' });
		expect(screen.getAllByRole('button', { expanded: true }).map((b) => b.id)).toEqual([
			'help-heading-review'
		]);
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Got it' }));
		expect(dialog.isConnected).toBe(false);
		expect(tips.seen('review')).toBe(false);
	});
});
