// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { encode } from 'uqr';
import { describe, expect, it } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import QrCode from './QrCode.svelte';

const QUIET = 4;

/** Rebuilds the module grid from the drawn path ("M x y h n v1 h-n z" per run of dark modules). */
function cellsOf(path: string): Set<string> {
	const cells = new Set<string>();
	for (const [, x, y, length] of path.matchAll(/M(\d+) (\d+)h(\d+)v1h-\d+z/g)) {
		for (let i = 0; i < Number(length); i++) cells.add(`${Number(x) + i},${y}`);
	}
	return cells;
}

describe('QrCode', () => {
	it('draws exactly the modules of the code, with a quiet zone, dark on white', async () => {
		i18n.set('es');
		const value = 'https://tuttifrutti.aresius.xyz/KT7P';
		render(QrCode, { value, label: 'QR de prueba' });
		const svg = await screen.findByRole('img', { name: 'QR de prueba' });

		const matrix = encode(value, { ecc: 'M', border: 0 }).data;
		const expected = new Set<string>();
		matrix.forEach((row, y) =>
			row.forEach((dark, x) => dark && expected.add(`${x + QUIET},${y + QUIET}`))
		);
		expect(cellsOf(svg.querySelector('path')!.getAttribute('d')!)).toEqual(expected);

		const size = matrix.length + QUIET * 2;
		expect(svg.getAttribute('viewBox')).toBe(`0 0 ${size} ${size}`);
		expect(svg.querySelector('path')!.getAttribute('fill')).toBe('#1a1033');
		expect(svg.classList.contains('bg-white')).toBe(true);
	});

	it('shows a placeholder while the generator loads', () => {
		i18n.set('es');
		render(QrCode, { value: 'x', label: 'QR' });
		expect(screen.getByRole('status', { name: 'Generando el QR…' })).toBeTruthy();
	});
});
