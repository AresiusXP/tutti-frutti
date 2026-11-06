// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { claimBack } from './sheetBack';

describe('claimBack without a router', () => {
	it('is inert (unit tests, static render): sheets then work without Back handling', async () => {
		const back = vi.spyOn(history, 'back');
		const claim = claimBack();
		await Promise.resolve();
		expect(claim?.gone).toBe(false);
		claim?.release();
		expect(back).not.toHaveBeenCalled();
		back.mockRestore();
	});
});
