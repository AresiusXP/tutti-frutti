import { describe, expect, it } from 'vitest';
import type { Cell } from '$lib/protocol';
import { classifyCell } from './badge';

const cell = (over: Partial<Cell>): Cell => ({
	raw: 'x',
	answer: 'x',
	status: 'valid',
	points: 10,
	shared: false,
	...over
});

describe('classifyCell', () => {
	it('shows nothing before there is a result', () => {
		expect(classifyCell(undefined)).toEqual({
			kind: 'pending',
			points: 0,
			tone: null,
			struck: false,
			chip: null
		});
	});

	it('a unique valid answer is worth 10 and lime', () => {
		expect(classifyCell(cell({}))).toMatchObject({ kind: 'unique', points: 10, tone: 'lime' });
	});

	it('a shared valid answer is worth 5 and banana', () => {
		expect(classifyCell(cell({ points: 5, shared: true }))).toMatchObject({
			kind: 'shared',
			points: 5,
			tone: 'banana',
			struck: false,
			chip: null
		});
	});

	it('an empty answer has no pill', () => {
		expect(classifyCell(cell({ status: 'empty', points: 0, raw: '' }))).toMatchObject({
			kind: 'empty',
			tone: null,
			chip: null
		});
	});

	it('a wrong-letter answer is grey with its chip', () => {
		expect(classifyCell(cell({ status: 'wrong_letter', points: 0 }))).toMatchObject({
			kind: 'wrong_letter',
			points: 0,
			tone: 'grey',
			chip: 'wrong_letter',
			struck: false
		});
	});

	it('a rejected answer is grey, struck through and tagged', () => {
		expect(classifyCell(cell({ status: 'rejected', points: 0 }))).toMatchObject({
			kind: 'rejected',
			tone: 'grey',
			chip: 'rejected',
			struck: true
		});
	});

	it('a valid cell without points is grey (defensive)', () => {
		expect(classifyCell(cell({ points: 0 }))).toMatchObject({ kind: 'zero', tone: 'grey' });
	});
});
