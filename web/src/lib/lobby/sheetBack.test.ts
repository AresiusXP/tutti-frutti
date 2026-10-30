// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { claimBack } from './sheetBack';

describe('claimBack without a router', () => {
	it('is not available (unit tests, static render): sheets then work without Back handling', () => {
		expect(claimBack()).toBeNull();
	});
});
