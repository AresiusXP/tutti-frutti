import { describe, expect, it } from 'vitest';
import { uiAvatar } from './avatar';

describe('uiAvatar', () => {
	it('keeps a palette colour and the emoji', () => {
		expect(uiAvatar({ emoji: '🍓', color: 'strawberry' })).toEqual({
			emoji: '🍓',
			color: 'strawberry'
		});
	});

	it('falls back to the default colour for anything else', () => {
		expect(uiAvatar({ emoji: '🍌', color: '#ff4f81' }).color).toBe('banana');
		expect(uiAvatar({ emoji: '🍌', color: '' }).color).toBe('banana');
	});
});
