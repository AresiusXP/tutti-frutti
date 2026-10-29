import type { State } from '../../web/src/lib/protocol/protocol';
import { test, expect } from '../src/fixtures';
import { joinByInviteLink, uniqueAnswers } from '../src/flows';
import { createRoomAPI, joinRoomSocket, publicRooms } from '../src/rooms';

// Poll already-received snapshots, never a busy loop or a modified browser/server clock.
const REAL_TIME = { timeout: 65_000, intervals: [250, 500, 1000] };

function expectOrdinarySeats(state: State): void {
	// The public wire view must not disclose internal automated-seat metadata.
	expect(state.view).not.toHaveProperty('automated');
	for (const player of state.view.players) {
		expect(Object.keys(player).sort()).toEqual([
			'avatar',
			'connected',
			'connectedAt',
			'id',
			'joinedRound',
			'joinedSeq',
			'left',
			'name',
			'totalScore'
		]);
		expect(player.name).not.toMatch(
			/\b(bot|robot|automated|computer|automático|geautomatiseerd)\b/i
		);
	}
}

for (const locale of ['es', 'en', 'nl'] as const) {
	test(`${locale}: production fallback fills naturally and completes a casual game`, async ({
		app
	}) => {
		test.setTimeout(180_000);
		const host = await app.player('Host', { locale });
		const code = await createRoomAPI(app, host, 'public');
		const socket = await joinRoomSocket(app, host, code);
		const joinedAt = socket.state!.serverTime;
		const hostID = socket.state!.view.youId;
		expect(socket.state!.view.settings.timeLimitSeconds).toBe(90);
		expect(socket.state!.view.players).toHaveLength(1);
		socket.send('update_settings', { settings: { ...socket.state!.view.settings, rounds: 2 } });
		await expect.poll(() => socket.state?.view.settings.rounds).toBe(2);

		await expect.poll(() => socket.state?.view.players.length, REAL_TIME).toBeGreaterThanOrEqual(2);
		const firstFill = socket.states.find((state) => state.view.players.length === 2)!;
		// One second of transport tolerance around the server's 20–30 s production range.
		expect(firstFill.serverTime - joinedAt).toBeGreaterThanOrEqual(19_000);
		expect(firstFill.serverTime - joinedAt).toBeLessThanOrEqual(31_000);
		await expect.poll(() => socket.state?.view.players.length, REAL_TIME).toBe(3);
		expectOrdinarySeats(socket.state!);
		const automated = socket.state!.view.players.filter((player) => player.id !== hostID);

		socket.send('start_game');
		await expect.poll(() => socket.state?.view.phase).toBe('letter');
		socket.send('stop_letter');
		await expect.poll(() => socket.state?.view.phase).toBe('playing');
		const letter = socket.state!.view.round!.letter;
		// Leave a field empty to exercise ask_stop, whose electorate must exclude both seats.
		for (const [category, text] of uniqueAnswers(letter, host, 5).entries()) {
			socket.send('set_answer', { category, text });
		}
		await expect.poll(() => socket.state?.view.round?.filled[hostID]).toBe(5);
		await expect
			.poll(
				() => automated.some((player) => (socket.state?.view.round?.filled[player.id] ?? 0) > 0),
				REAL_TIME
			)
			.toBe(true);
		expect(socket.state!.view.phase).toBe('playing');
		for (const player of automated) {
			expect(socket.state!.view.round!.answers).not.toHaveProperty(player.id);
		}
		socket.send('ask_stop');
		await expect.poll(() => socket.state?.view.phase).toBe('grace');
		expect(socket.state!.view.round!.callerId).toBe(hostID);
		await expect.poll(() => socket.state?.view.phase).toBe('review');
		expect(socket.state!.view.round!.reviewVoters).toEqual([hostID]);

		const target = automated.find((player) =>
			socket.state!.view.round!.answers[player.id]!.some(Boolean)
		)!;
		const category = socket.state!.view.round!.answers[target.id]!.findIndex(Boolean);
		expect(category).toBeGreaterThanOrEqual(0);
		expect(socket.state!.view.round!.result!.players[target.id]!.cells[category]!.status).toBe(
			'valid'
		);
		socket.send('vote_reject', { category, targetPlayer: target.id });
		// One human vote is a majority even with the other automated seat connected.
		await expect
			.poll(() => socket.state?.view.round?.result?.players[target.id]?.cells[category]?.status)
			.toBe('rejected');
		expect(socket.state!.view.round!.rejectVotes.map((vote) => vote.voter)).toEqual([hostID]);
		socket.send('finish_review');
		await expect.poll(() => socket.state?.view.phase).toBe('round_summary');
		expect(socket.state!.view.history).toHaveLength(1);
		expect(socket.state!.view.history[0]!.players[hostID]!.total).toBe(50);

		socket.send('next_round');
		await expect.poll(() => socket.state?.view.roundNum).toBe(2);
		expect(automated.map((player) => player.id)).toContain(socket.state!.view.chooserId);
		// The automated chooser really taps; no test-side time travel or stop_letter command.
		await expect.poll(() => socket.state?.view.phase).toBe('playing');
		socket.send('ask_stop');
		await expect.poll(() => socket.state?.view.phase).toBe('review');
		socket.send('finish_review');
		await expect.poll(() => socket.state?.view.phase).toBe('round_summary');
		expect(socket.state!.view.history).toHaveLength(2);
		expectOrdinarySeats(socket.state!);
		expect(socket.errors).toEqual([]);
		expect((await publicRooms(host)).map((room) => room.code)).not.toContain(code);

		// Reclaim the host seat before final (a fresh final invite is blocked by the gate).
		await joinByInviteLink(host, code, { landing: 'summary' });
		await host.summary.endGame();
		await host.final.expectVisible();
		await expect(host.page.getByText(host.t('rooms_casual'), { exact: true })).toBeVisible();
		expect(await host.readableText()).not.toMatch(
			/\b(bot|robot|automated|computer|automático|geautomatiseerd)\b/i
		);
	});
}

test('human lobby arrivals cancel pending fill and prune existing fallback seats', async ({
	app
}) => {
	test.setTimeout(120_000);
	const waitingHost = await app.player('Waiting', { locale: 'en' });
	const waitingCode = await createRoomAPI(app, waitingHost, 'public');
	const waiting = await joinRoomSocket(app, waitingHost, waitingCode);
	const arrivedEarly = await app.player('Early', { locale: 'en' });
	await joinRoomSocket(app, arrivedEarly, waitingCode);
	await expect.poll(() => waiting.state?.view.players.length).toBe(2);
	const cancelAt = waiting.state!.serverTime;
	const arrivalIndex = waiting.states.length - 1;

	const host = await app.player('Host', { locale: 'en' });
	const code = await createRoomAPI(app, host, 'public');
	const socket = await joinRoomSocket(app, host, code);
	await expect.poll(() => socket.state?.view.players.length, REAL_TIME).toBe(3);
	const automatedIDs = socket
		.state!.view.players.filter((player) => player.id !== socket.state!.view.youId)
		.map((player) => player.id);
	const second = await app.player('Second', { locale: 'en' });
	const secondSocket = await joinRoomSocket(app, second, code);
	await expect
		.poll(
			() =>
				socket.state?.view.players
					.map((player) => player.id)
					.filter((id) => automatedIDs.includes(id)).length
		)
		.toBe(1);
	const third = await app.player('Third', { locale: 'en' });
	const thirdSocket = await joinRoomSocket(app, third, code);
	await expect
		.poll(() => socket.state?.view.players.map((player) => player.id))
		.toEqual([
			socket.state!.view.youId,
			secondSocket.state!.view.youId,
			thirdSocket.state!.view.youId
		]);
	// Pongs advance the observed server clock past BOTH production fill deadlines (max 55 s).
	await expect.poll(() => waiting.serverTime - cancelAt, REAL_TIME).toBeGreaterThan(55_000);
	const afterArrival = waiting.states.slice(arrivalIndex);
	expect(afterArrival.every((state) => state.view.players.length === 2)).toBe(true);
	expect(waiting.state!.view.players.map((player) => player.name)).toEqual(['Waiting', 'Early']);
	expect(waiting.errors).toEqual([]);
	expect(socket.errors).toEqual([]);
});
