// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { ReportDraft } from './report.svelte';
import { apiError, deferred, FakeSafetyApi } from './safety.testkit';
import type { ReportTarget } from './targets';

const target: ReportTarget = {
	kind: 'answer',
	person: { id: 'p2', name: 'Beto' },
	round: 2,
	category: 1,
	text: 'Perro'
};

function setup(over: { instance?: string | null } = {}) {
	const api = new FakeSafetyApi();
	let instance: string | null = over.instance === undefined ? 'room-instance-9' : over.instance;
	const draft = new ReportDraft(target, () => ({ code: 'KT7P', roomInstanceId: instance }), {
		api,
		newKey: () => 'key-abcdefgh'
	});
	return { api, draft, setInstance: (id: string | null) => (instance = id) };
}

describe('ReportDraft', () => {
	it('starts empty and needs a reason before anything is sent', async () => {
		const { draft, api } = setup();
		expect(draft.status).toBe('idle');
		expect(await draft.send()).toBe(false);
		expect(draft.missingReason).toBe(true);
		expect(draft.status).toBe('idle');
		expect(api.reports).toHaveLength(0);
	});

	it('sends the documented payload, with the room instance and the code of the current state', async () => {
		const { draft, api } = setup();
		draft.reason = 'harassment';
		draft.comment = 'rude';
		expect(await draft.send()).toBe(true);
		expect(api.reports).toEqual([
			{
				kind: 'answer',
				roomInstanceId: 'room-instance-9',
				code: 'KT7P',
				ref: { playerId: 'p2', round: 2, category: 1 },
				reason: 'harassment',
				comment: 'rude',
				idempotencyKey: 'key-abcdefgh',
				observedText: 'Perro'
			}
		]);
		expect(draft.status).toBe('sent');
	});

	it('never says "sent" before the server answered', async () => {
		const { draft, api } = setup();
		const pending = deferred<{ id: string }>();
		api.reportAnswers = [pending.promise];
		draft.reason = 'spam';
		const sending = draft.send();
		expect(draft.status).toBe('sending');
		pending.resolve({ id: 'r1' });
		expect(await sending).toBe(true);
		expect(draft.status).toBe('sent');
	});

	it('ignores a second Send while one is in flight', async () => {
		const { draft, api } = setup();
		const pending = deferred<{ id: string }>();
		api.reportAnswers = [pending.promise];
		draft.reason = 'spam';
		const first = draft.send();
		const second = draft.send();
		pending.resolve({ id: 'r1' });
		await Promise.all([first, second]);
		expect(api.reports).toHaveLength(1);
	});

	it.each([
		[429, 'rate_limited'],
		[503, 'server_busy'],
		[0, 'network'],
		[400, 'invalid_report'],
		[403, 'not_in_room']
	])('a %s (%s) fails the send, keeps the draft and the error code', async (status, code) => {
		const { draft, api } = setup();
		api.reportAnswers = [apiError(status, code)];
		draft.reason = 'hate';
		draft.comment = 'keep me';
		expect(await draft.send()).toBe(false);
		expect(draft.status).toBe('failed');
		expect(draft.error).toBe(code);
		expect([draft.reason, draft.comment]).toEqual(['hate', 'keep me']);
	});

	it('a retry reuses the SAME idempotency key (one report even if the first answer was lost)', async () => {
		const { draft, api } = setup();
		api.reportAnswers = [apiError(503, 'server_busy'), { id: 'r1' }];
		draft.reason = 'violence';
		expect(await draft.send()).toBe(false);
		draft.comment = 'added after the failure';
		expect(await draft.send()).toBe(true);
		expect(api.reports.map((r) => r.idempotencyKey)).toEqual(['key-abcdefgh', 'key-abcdefgh']);
		expect(api.reports[1].comment).toBe('added after the failure');
		expect(draft.error).toBeNull();
	});

	it('another draft is another report: it gets another key', () => {
		const api = new FakeSafetyApi();
		const ctx = () => ({ code: 'KT7P', roomInstanceId: null });
		const a = new ReportDraft(target, ctx, { api });
		const b = new ReportDraft(target, ctx, { api });
		expect(a.key).not.toBe(b.key);
		expect(a.key).toMatch(/^[A-Za-z0-9_-]{8,64}$/);
	});

	it('reads the room instance when it sends (the room may have restarted since the sheet opened)', async () => {
		const { draft, api, setInstance } = setup();
		draft.reason = 'other';
		setInstance(null);
		await draft.send();
		expect(api.reports[0].roomInstanceId).toBeUndefined();
		expect(api.reports[0].code).toBe('KT7P');
	});

	it('counts the characters left of the comment in code points', () => {
		const { draft } = setup();
		draft.comment = '🍓🍓';
		expect(draft.remaining).toBe(278);
	});
});
