import { test, expect } from '../src/fixtures';
import { createRoom, joinByInviteLink } from '../src/flows';
import { createRoomAPI, joinRoomSocket, publicRooms } from '../src/rooms';

for (const locale of ['es', 'en', 'nl'] as const) {
	test(`${locale}: directory lists only claimed public lobbies and filters language`, async ({
		app
	}) => {
		const host = await app.player('Host', { locale });
		const privateHost = await app.player('Private', { locale });
		const code = await createRoomAPI(app, host, 'public');
		const privateCode = await createRoomAPI(app, privateHost);
		const privateSocket = await joinRoomSocket(app, privateHost, privateCode);
		expect(privateSocket.state?.visibility).toBe('private');
		expect(privateSocket.state?.view.settings.timeLimitSeconds).toBe(0);
		expect((await publicRooms(host)).map((room) => room.code)).not.toContain(code);

		const socket = await joinRoomSocket(app, host, code);
		expect(socket.state?.visibility).toBe('public');
		expect(socket.state?.view.settings.timeLimitSeconds).toBe(90);
		await expect
			.poll(async () => (await publicRooms(host, locale)).find((room) => room.code === code))
			.toEqual({
				code,
				lang: locale,
				players: 1,
				maxPlayers: 10,
				categoryLabels: socket.state?.view.categoryLabels,
				rounds: 5,
				timeLimitSeconds: 90
			});
		expect((await publicRooms(host)).map((room) => room.code)).not.toContain(privateCode);
		const otherLanguage = locale === 'en' ? 'nl' : 'en';
		expect((await publicRooms(host, otherLanguage)).map((room) => room.code)).not.toContain(code);

		const guest = await app.player('Guest', { locale });
		await joinRoomSocket(app, guest, code);
		socket.send('start_game');
		await expect.poll(() => socket.state?.view.phase).toBe('letter');
		await expect
			.poll(async () => (await publicRooms(host)).map((room) => room.code))
			.not.toContain(code);
	});

	test(`${locale}: create public, browse and join; private invites stay private`, async ({
		app
	}) => {
		const host = await app.player('Host', { locale });
		const privateHost = await app.player('Private', { locale });
		const privateCode = await createRoomAPI(app, privateHost, 'private');
		await joinRoomSocket(app, privateHost, privateCode);
		const code = await createRoom(app, host, undefined, 'public');
		await expect(host.page.getByText(host.t('rooms_casual'), { exact: true })).toBeVisible();

		const visitor = await app.player('Visitor', { locale });
		await visitor.rooms.open();
		await expect(visitor.rooms.joinButton(code)).toBeVisible();
		await expect(visitor.rooms.card(code)).toContainText(visitor.t('rooms_time', { seconds: 90 }));
		await expect(visitor.rooms.joinButton(privateCode)).toHaveCount(0);
		await visitor.rooms.joinButton(code).click();
		await visitor.gate.expectOpen(code);
		await visitor.gate.setNickname(visitor.name);
		await visitor.gate.join();
		await visitor.lobby.expectVisible(code);
		await expect(visitor.page.getByText(visitor.t('rooms_casual'), { exact: true })).toBeVisible();
		await host.lobby.expectPlayers([host.name, visitor.name]);

		const invited = await app.player('Invited', { locale });
		await joinByInviteLink(invited, privateCode);
		await expect(invited.page.getByText(invited.t('rooms_casual'), { exact: true })).toHaveCount(0);
	});
}
