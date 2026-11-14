// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { i18n } from '#lib/i18n.svelte.ts';
import HelpButton from './HelpButton.svelte';

beforeEach(() => i18n.set('en'));
afterEach(() => cleanup());

describe('HelpButton', () => {
	it('the round icon is a 48 px button named "How to play" that opens a dialog', async () => {
		render(HelpButton, {});
		const button = screen.getByRole('button', { name: 'How to play' });
		expect(button.getAttribute('aria-haspopup')).toBe('dialog');
		expect(button.className).toContain('size-12');
		expect(screen.queryByRole('dialog')).toBeNull();
		await fireEvent.click(button);
		expect(screen.getByRole('dialog', { name: 'How to play' })).toBeTruthy();
	});

	it('the text link shows its own name and is 48 px tall', () => {
		render(HelpButton, { variant: 'text' });
		const button = screen.getByRole('button', { name: 'How to play' });
		expect(button.textContent?.trim()).toBe('How to play');
		expect(button.className).toContain('h-12');
	});

	it('opens the guide at the part of the screen it sits on', async () => {
		render(HelpButton, { section: 'review' });
		await fireEvent.click(screen.getByRole('button', { name: 'How to play' }));
		const open = screen.getAllByRole('button', { expanded: true });
		expect(open.map((b) => b.id)).toEqual(['help-heading-review']);
	});

	it('closing the guide leaves the button where it was and gives it the focus back', async () => {
		render(HelpButton, {});
		const button = screen.getByRole('button', { name: 'How to play' });
		button.focus();
		await fireEvent.click(button);
		await fireEvent.click(screen.getByRole('button', { name: 'Got it' }));
		expect(screen.queryByRole('dialog')).toBeNull();
		expect(document.activeElement).toBe(button);
	});
});
