// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AVATAR_COLORS, AVATAR_EMOJIS } from '$lib/avatar';
import { i18n } from '$lib/i18n.svelte';
import AvatarPicker from './AvatarPicker.svelte';

describe('AvatarPicker', () => {
	beforeEach(() => i18n.set('es'));

	it('exposes two labelled groups of toggle buttons, not radios', () => {
		render(AvatarPicker, { avatar: { emoji: '🍌', color: 'banana' }, onchange: () => {} });
		const emojis = screen.getByRole('group', { name: 'Elegí un emoji' });
		const colors = screen.getByRole('group', { name: 'Elegí un color' });
		expect(within(emojis).getAllByRole('button')).toHaveLength(AVATAR_EMOJIS.length);
		expect(within(colors).getAllByRole('button')).toHaveLength(AVATAR_COLORS.length);
		expect(screen.queryAllByRole('radio')).toHaveLength(0);
		expect(screen.queryAllByRole('radiogroup')).toHaveLength(0);
		for (const button of screen.getAllByRole('button')) {
			expect(button.getAttribute('type')).toBe('button');
			expect(button.hasAttribute('aria-pressed')).toBe(true);
		}
	});

	it('marks only the current emoji and colour as pressed', () => {
		render(AvatarPicker, { avatar: { emoji: '🍓', color: 'strawberry' }, onchange: () => {} });
		const pressed = screen.getAllByRole('button', { pressed: true });
		expect(pressed).toHaveLength(2);
		expect(pressed.map((b) => b.textContent?.trim() || b.getAttribute('aria-label'))).toEqual([
			'🍓',
			'Frutilla'
		]);
	});

	it('names colour buttons in the current language', () => {
		i18n.set('en');
		render(AvatarPicker, { avatar: { emoji: '🍌', color: 'banana' }, onchange: () => {} });
		expect(screen.getByRole('group', { name: 'Pick a colour' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Strawberry' })).toBeTruthy();
	});

	it('reports the picked emoji or colour', async () => {
		const onchange = vi.fn();
		render(AvatarPicker, { avatar: { emoji: '🍌', color: 'banana' }, onchange });
		await fireEvent.click(screen.getByRole('button', { name: '🍇' }));
		expect(onchange).toHaveBeenLastCalledWith({ emoji: '🍇' });
		await fireEvent.click(screen.getByRole('button', { name: 'Uva' }));
		expect(onchange).toHaveBeenLastCalledWith({ color: 'grape' });
	});

	it('keeps every button naturally focusable (no roving tabindex needed)', () => {
		render(AvatarPicker, { avatar: { emoji: '🍌', color: 'banana' }, onchange: () => {} });
		for (const button of screen.getAllByRole('button')) {
			expect(button.getAttribute('tabindex')).toBeNull();
		}
	});
});
