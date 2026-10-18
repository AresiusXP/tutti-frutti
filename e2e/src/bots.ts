import WebSocket from 'ws';
import { BASE_URL, ORIGIN } from './env';

const PROTOCOL_VERSION = 3;

/**
 * A cheap player without a browser: its own guest cookie and one WebSocket that joins a room.
 * Used to fill a lobby; it speaks the same protocol as the web app (docs/DESIGN.md §9a).
 */
export class Bot {
	#socket?: WebSocket;
	closeCode: number | undefined;
	lastEvent: string | undefined;
	/** The `code` of every `error` frame the server sent (e.g. `not_participant`). */
	readonly errors: string[] = [];

	private constructor(
		readonly name: string,
		private readonly cookie: string
	) {}

	static async join(code: string, name: string): Promise<Bot> {
		const session = await fetch(`${BASE_URL}/api/session`);
		const cookie = session.headers
			.getSetCookie()
			.map((c) => c.split(';')[0])
			.find((c) => c?.startsWith('tf_guest='));
		if (!cookie) throw new Error('the server did not hand out a guest cookie');
		const bot = new Bot(name, cookie);
		await bot.#connect(code);
		return bot;
	}

	#connect(code: string): Promise<void> {
		const url = `${BASE_URL.replace(/^http/, 'ws')}/ws`;
		const socket = new WebSocket(url, { headers: { Origin: ORIGIN, Cookie: this.cookie } });
		this.#socket = socket;
		return new Promise((resolve, reject) => {
			const timer = setTimeout(
				() => reject(new Error(`${this.name}: no state within 10 s`)),
				10_000
			);
			socket.on('open', () => {
				socket.send(
					JSON.stringify({
						t: 'join',
						v: PROTOCOL_VERSION,
						d: { code, name: this.name, avatar: { emoji: '🍌', color: 'banana' } }
					})
				);
			});
			socket.on('message', (data) => {
				const frame = JSON.parse(data.toString()) as {
					t: string;
					d?: { type?: string; code?: string };
				};
				if (frame.t === 'state') {
					clearTimeout(timer);
					resolve();
				} else if (frame.t === 'event') {
					this.lastEvent = frame.d?.type;
				} else if (frame.t === 'error') {
					this.errors.push(frame.d?.code ?? '');
					clearTimeout(timer);
					reject(new Error(`${this.name}: ${JSON.stringify(frame.d)}`));
				}
			});
			socket.on('close', (closeCode) => {
				this.closeCode = closeCode;
			});
			socket.on('error', (error) => {
				clearTimeout(timer);
				reject(error);
			});
		});
	}

	/** Sends a command (`ask_stop`, `vote_stop`, ...); the server answers with state or an `error` frame. */
	send(type: string, payload?: unknown): void {
		this.#socket?.send(JSON.stringify({ t: type, v: PROTOCOL_VERSION, d: payload }));
	}

	close(): void {
		this.#socket?.close();
	}
}

/** Joins `count` bots, a few at a time. */
export async function joinBots(code: string, names: string[]): Promise<Bot[]> {
	const bots: Bot[] = [];
	for (const name of names) bots.push(await Bot.join(code, name));
	return bots;
}
