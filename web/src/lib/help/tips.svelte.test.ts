import { describe, expect, it } from 'vitest';
import { TIPS_KEY, TipsStore, type TipStorage } from './tips.svelte.ts';

function memory(initial?: string): TipStorage & { data: Map<string, string> } {
	const data = new Map<string, string>(initial === undefined ? [] : [[TIPS_KEY, initial]]);
	return {
		data,
		getItem: (key) => data.get(key) ?? null,
		setItem: (key, value) => void data.set(key, value),
		removeItem: (key) => void data.delete(key)
	};
}

describe('TipsStore', () => {
	it('shows every tip to a browser that has seen none', () => {
		const tips = new TipsStore(memory());
		expect(tips.seen('playing')).toBe(false);
		expect(tips.seen('review')).toBe(false);
	});

	it('remembers a seen tip across reloads, and only that one', () => {
		const storage = memory();
		const first = new TipsStore(storage);
		first.markSeen('review');
		expect(storage.data.get(TIPS_KEY)).toBe('["review"]');

		const reloaded = new TipsStore(storage);
		expect(reloaded.seen('review')).toBe(true);
		expect(reloaded.seen('playing')).toBe(false);
	});

	it('does not write again for a tip that was already seen', () => {
		const storage = memory('["playing"]');
		let writes = 0;
		const tips = new TipsStore({
			...storage,
			setItem: (key, value) => {
				writes++;
				storage.setItem(key, value);
			}
		});
		tips.markSeen('playing');
		expect(writes).toBe(0);
	});

	it('ignores ids it does not know and anything that is not a list', () => {
		expect(new TipsStore(memory('["playing","nope",3,null]')).seen('playing')).toBe(true);
		expect(new TipsStore(memory('["nope"]')).seen('review')).toBe(false);
		expect(new TipsStore(memory('{"playing":true}')).seen('playing')).toBe(false);
		expect(new TipsStore(memory('not json')).seen('playing')).toBe(false);
	});

	it('works without storage and when the storage throws', () => {
		const none = new TipsStore(null);
		none.markSeen('playing');
		expect(none.seen('playing')).toBe(true);

		const broken: TipStorage = {
			getItem: () => {
				throw new Error('blocked');
			},
			setItem: () => {
				throw new Error('full');
			},
			removeItem: () => {
				throw new Error('blocked');
			}
		};
		const tips = new TipsStore(broken);
		expect(tips.seen('review')).toBe(false);
		tips.markSeen('review');
		expect(tips.seen('review')).toBe(true);
		expect(() => tips.reset()).not.toThrow();
		expect(tips.seen('review')).toBe(false);
	});

	it('reset() brings every tip back and forgets the stored list', () => {
		const storage = memory('["playing","review"]');
		const tips = new TipsStore(storage);
		tips.reset();
		expect(tips.seen('playing')).toBe(false);
		expect(storage.data.has(TIPS_KEY)).toBe(false);
	});
});
