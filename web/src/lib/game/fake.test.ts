// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { FakeSession } from './fake.svelte';
import { makeRound, makeSettings, makeStopVote, makeView } from './fixtures';

describe('FakeSession: vote to stop', () => {
	it('records askStop and voteStop with their arguments, in order', () => {
		const session = new FakeSession(makeView());
		session.askStop();
		session.voteStop(true);
		session.voteStop(false);
		expect(session.sent).toEqual([
			{ cmd: 'askStop', args: [] },
			{ cmd: 'voteStop', args: [true] },
			{ cmd: 'voteStop', args: [false] }
		]);
	});

	it('is driven by state: setView swaps the open vote, emit adds the events', () => {
		const session = new FakeSession(
			makeView({
				phase: 'playing',
				youId: 'p2',
				round: makeRound({ participants: ['p1', 'p2', 'p3'], stopVote: makeStopVote() })
			})
		);
		expect(session.isParticipant).toBe(true);
		expect(session.view?.round?.stopVote?.voters).toEqual(['p2', 'p3']);
		session.emit({ type: 'stop_rejected', playerId: 'p1', name: 'Ana', reason: 'timeout' });
		expect(session.events).toHaveLength(1);
		expect(session.events[0]).toMatchObject({ type: 'stop_rejected', reason: 'timeout', seq: 1 });
		session.setView({ ...session.view!, round: makeRound({ participants: ['p1', 'p2', 'p3'] }) });
		expect(session.view?.round?.stopVote).toBeUndefined();
	});

	it('fixtures: stopVote is on by default and can be switched off', () => {
		expect(makeSettings().stopVote).toBe(true);
		expect(makeSettings({ stopVote: false }).stopVote).toBe(false);
		expect(makeStopVote({ yes: ['p2'] })).toMatchObject({ callerId: 'p1', yes: ['p2'] });
	});
});
