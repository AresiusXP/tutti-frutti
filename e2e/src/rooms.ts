import type { PublicRoomsResponse } from '../../web/src/lib/protocol/api';
import { Bot } from './bots';
import { ORIGIN } from './env';
import { expect, type App } from './fixtures';
import type { Locale } from './messages';
import type { Player } from './player';

/** REST creation alone does not publish a room: joinRoomSocket claims the host seat. */
export async function createRoomAPI(
	app: App,
	host: Player,
	visibility?: 'private' | 'public'
): Promise<string> {
	await app.beforeRoomCreation();
	const response = await host.context.request.post('/api/rooms', {
		headers: { Origin: ORIGIN },
		data: {
			name: host.name,
			avatar: { emoji: '🍓', color: 'strawberry' },
			lang: host.locale,
			...(visibility ? { visibility } : {})
		}
	});
	expect(response.ok()).toBe(true);
	return ((await response.json()) as { code: string }).code;
}

export async function joinRoomSocket(app: App, player: Player, code: string): Promise<Bot> {
	await player.context.request.get('/api/session');
	const cookie = (await player.context.cookies()).find((item) => item.name === 'tf_guest');
	if (!cookie) throw new Error('missing guest cookie');
	const client = await Bot.join(code, player.name, `${cookie.name}=${cookie.value}`);
	app.onDispose(() => client.close());
	return client;
}

export async function publicRooms(
	player: Player,
	lang?: Locale
): Promise<PublicRoomsResponse['rooms']> {
	const response = await player.context.request.get(`/api/rooms${lang ? `?lang=${lang}` : ''}`);
	expect(response.ok()).toBe(true);
	return ((await response.json()) as PublicRoomsResponse).rooms;
}
