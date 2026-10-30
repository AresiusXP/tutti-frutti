import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	buildReport,
	canTarget,
	clip,
	isBotId,
	MAX_COMMENT,
	MAX_OBSERVED,
	newIdempotencyKey,
	REPORT_REASONS
} from './targets';

const ctx = { code: 'KT7P', roomInstanceId: '0b7f6c0e-5a54-4d57-9d35-1c1a1c6f6a01' };
const ana = { id: 'p2', name: 'Ana' };
const KEY = 'draft-key-0001';

describe('buildReport', () => {
	it('player: names the player id, quotes the nickname, always sends the room instance and the code', () => {
		expect(
			buildReport({ kind: 'player', person: ana }, ctx, { reason: 'harassment', comment: '' }, KEY)
		).toEqual({
			kind: 'player',
			roomInstanceId: ctx.roomInstanceId,
			code: 'KT7P',
			ref: { playerId: 'p2' },
			reason: 'harassment',
			idempotencyKey: KEY,
			observedText: 'Ana'
		});
	});

	it('custom_label: the category index and the label as the reporter sees it', () => {
		const body = buildReport(
			{ kind: 'custom_label', category: 6, label: 'Superhéroe' },
			ctx,
			{ reason: 'other', comment: '  not nice  ' },
			KEY
		);
		expect(body).toMatchObject({
			kind: 'custom_label',
			ref: { category: 6, label: 'Superhéroe' },
			comment: 'not nice',
			observedText: 'Superhéroe'
		});
		expect(body.ref).not.toHaveProperty('playerId');
	});

	it.each(['answer', 'ai_verdict'] as const)(
		'%s: round + category + the author, and the text shown',
		(kind) => {
			const body = buildReport(
				{ kind, person: ana, round: 2, category: 1, text: 'text shown' },
				ctx,
				{ reason: 'spam', comment: '' },
				KEY
			);
			expect(body).toMatchObject({
				kind,
				ref: { playerId: 'p2', round: 2, category: 1 },
				observedText: 'text shown'
			});
		}
	);

	it('omits an empty comment and a missing room instance, never the code', () => {
		const body = buildReport(
			{ kind: 'player', person: ana },
			{ code: 'KT7P', roomInstanceId: null },
			{ reason: 'hate', comment: '   ' },
			KEY
		);
		expect(body).not.toHaveProperty('comment');
		expect(body.roomInstanceId).toBeUndefined();
		expect(body.code).toBe('KT7P');
		expect(JSON.parse(JSON.stringify(body))).not.toHaveProperty('roomInstanceId');
	});

	it('cuts the comment (280) and the observed text (200) in code points, never in the middle of an emoji', () => {
		const emoji = '🍓'.repeat(300);
		const body = buildReport(
			{ kind: 'answer', person: ana, round: 1, category: 0, text: emoji },
			ctx,
			{ reason: 'other', comment: emoji },
			KEY
		);
		expect([...(body.comment ?? '')]).toHaveLength(MAX_COMMENT);
		expect([...(body.observedText ?? '')]).toHaveLength(MAX_OBSERVED);
	});

	it('sends no fields the server refuses (no target, no time, no account)', () => {
		const body = buildReport(
			{ kind: 'player', person: ana },
			ctx,
			{ reason: 'hate', comment: 'x' },
			KEY
		);
		expect(Object.keys(body).sort()).toEqual(
			[
				'code',
				'comment',
				'idempotencyKey',
				'kind',
				'observedText',
				'reason',
				'ref',
				'roomInstanceId'
			].sort()
		);
	});
});

describe('reasons', () => {
	it('are exactly the eight the server accepts, in the order they are shown', () => {
		expect([...REPORT_REASONS]).toEqual([
			'harassment',
			'hate',
			'sexual',
			'violence',
			'self_harm',
			'spam',
			'impersonation',
			'other'
		]);
	});
});

describe('who can be targeted', () => {
	it('never oneself, nobody without an id, nor an automated seat', () => {
		expect(isBotId('bot:7f1c')).toBe(true);
		expect(isBotId('p2')).toBe(false);
		expect(canTarget('p1', 'p2')).toBe(true);
		expect(canTarget('p1', 'p1')).toBe(false);
		expect(canTarget('p1', '')).toBe(false);
		expect(canTarget('p1', 'bot:7f1c')).toBe(false);
	});
});

describe('clip', () => {
	it('keeps short text and cuts long text by code points', () => {
		expect(clip('abc', 5)).toBe('abc');
		expect(clip('abcdef', 3)).toBe('abc');
		expect(clip('🍓🍌🍉', 2)).toBe('🍓🍌');
	});
});

describe('newIdempotencyKey', () => {
	const valid = /^[A-Za-z0-9_-]{8,64}$/;
	afterEach(() => vi.unstubAllGlobals());

	it('is a UUID where the browser has crypto.randomUUID, and the server accepts its shape', () => {
		const key = newIdempotencyKey();
		expect(key).toMatch(valid);
		expect(newIdempotencyKey()).not.toBe(key);
	});

	it('falls back to getRandomValues, then to Math.random, with the same alphabet', () => {
		vi.stubGlobal('crypto', { getRandomValues: (a: Uint8Array) => a.fill(7) });
		expect(newIdempotencyKey()).toMatch(valid);
		vi.stubGlobal('crypto', undefined);
		const key = newIdempotencyKey();
		expect(key).toMatch(valid);
		expect(newIdempotencyKey()).not.toBe(key);
	});
});
