// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import RoomCodeInput from './RoomCodeInput.svelte';

const boxes = () => screen.getAllByRole<HTMLInputElement>('textbox');
const values = () => boxes().map((box) => box.value);

async function type(index: number, text: string) {
	const box = boxes()[index];
	box.focus();
	box.value = text;
	await fireEvent.input(box);
	await tick();
}

async function paste(index: number, text: string) {
	await fireEvent.paste(boxes()[index], { clipboardData: { getData: () => text } });
	await tick();
}

describe('RoomCodeInput', () => {
	beforeEach(() => i18n.set('es'));

	it('renders four labelled boxes', () => {
		render(RoomCodeInput, { value: '' });
		expect(boxes()).toHaveLength(4);
		expect(values()).toEqual(['', '', '', '']);
		expect(boxes()[0].getAttribute('aria-label')).toBe('Carácter 1 de 4');
	});

	it('uppercases typed characters and auto-advances', async () => {
		render(RoomCodeInput, { value: '' });
		await type(0, 'k');
		expect(values()).toEqual(['K', '', '', '']);
		expect(document.activeElement).toBe(boxes()[1]);
		await type(1, 't');
		expect(values()).toEqual(['K', 'T', '', '']);
		expect(document.activeElement).toBe(boxes()[2]);
	});

	it('rejects vowels, 0, 1, O and I and keeps focus', async () => {
		render(RoomCodeInput, { value: '' });
		for (const bad of ['a', 'e', 'i', 'o', 'u', '0', '1', '-']) {
			await type(0, bad);
			expect(values()).toEqual(['', '', '', '']);
			expect(boxes()[0].value).toBe('');
			expect(document.activeElement).toBe(boxes()[0]);
		}
	});

	it('normalises pasted text (case, spaces, invite links)', async () => {
		const oncomplete = vi.fn();
		render(RoomCodeInput, { value: '', oncomplete });
		await paste(0, ' kt 7p ');
		expect(values()).toEqual(['K', 'T', '7', 'P']);
		expect(oncomplete).toHaveBeenLastCalledWith('KT7P');

		await paste(2, 'https://tuttifrutti.aresius.xyz/bcd2');
		expect(values()).toEqual(['B', 'C', 'D', '2']);
		expect(oncomplete).toHaveBeenLastCalledWith('BCD2');
	});

	it('ignores pasted text that is not a code and keeps what was typed', async () => {
		const oncomplete = vi.fn();
		render(RoomCodeInput, { value: 'KT', oncomplete });
		for (const text of ['AEIOU 01', 'not a code', 'KT7PX', 'ßßKT', 'https://host/AEIO']) {
			await paste(0, text);
			expect(values(), text).toEqual(['K', 'T', '', '']);
		}
		expect(oncomplete).not.toHaveBeenCalled();
	});

	it('calls oncomplete once, when the fourth valid character is typed', async () => {
		const oncomplete = vi.fn();
		render(RoomCodeInput, { value: '', oncomplete });
		for (const [i, ch] of [...'KT7'].entries()) await type(i, ch);
		expect(oncomplete).not.toHaveBeenCalled();
		await type(3, 'p');
		expect(oncomplete).toHaveBeenCalledExactlyOnceWith('KT7P');
	});

	it('Backspace clears the box, or moves back when it is already empty', async () => {
		render(RoomCodeInput, { value: 'KT7' });
		boxes()[2].focus();
		await fireEvent.keyDown(boxes()[2], { key: 'Backspace' });
		await tick();
		expect(values()).toEqual(['K', 'T', '', '']);

		await fireEvent.keyDown(boxes()[2], { key: 'Backspace' });
		await tick();
		expect(values()).toEqual(['K', '', '', '']);
		expect(document.activeElement).toBe(boxes()[1]);
	});

	it('disables every box when disabled', () => {
		render(RoomCodeInput, { value: '', disabled: true });
		expect(boxes().every((box) => box.disabled)).toBe(true);
	});
});
