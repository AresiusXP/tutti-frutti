import WebSocket from 'ws';
import { Version as PROTOCOL_VERSION, type State } from '../../web/src/lib/protocol/protocol';
import { BASE_URL, ORIGIN } from './env';
import { acceptTermsWithCookie } from './terms';

/**
 * A cheap player without a browser: its own guest cookie and one WebSocket that joins a room.
 * Used to fill a lobby; it speaks the same protocol as the web app (docs/DESIGN.md §9a).
 */
export class Bot {
	#socket?: WebSocket;
	#heartbeat?: ReturnType<typeof setInterval>;
	state?: State;
	readonly states: State[] = [];
	serverTime = 0;
	closeCode: number | undefined;
	lastEvent: string | undefined;
	/** The `code` of every `error` frame the server sent (e.g. `not_participant`). */
	readonly errors: string[] = [];

	private constructor(
		readonly name: string,
		private readonly cookie: string
	) {}

	/**
	 * Joins `code` as a new guest (or as the guest of `existingCookie`, `tf_guest=...`). The guest accepts the
	 * terms first, like a player does: without them the server refuses the join (`terms_required`).
	 */
	static async join(code: string, name: string, existingCookie?: string): Promise<Bot> {
		let cookie = existingCookie;
		if (!cookie) {
			const session = await fetch(`${BASE_URL}/api/session`);
			cookie = session.headers
				.getSetCookie()
				.map((c) => c.split(';')[0])
				.find((c) => c?.startsWith('tf_guest='));
		}
		if (!cookie) throw new Error('the server did not hand out a guest cookie');
		await acceptTermsWithCookie(cookie);
		const bot = new Bot(name, cookie);
		try {
			await bot.#connect(code);
			return bot;
		} catch (error) {
			bot.close();
			throw error;
		}
	}

	#connect(code: string): Promise<void> {
		const url = `${BASE_URL.replace(/^http/, 'ws')}/ws`;
		const socket = new WebSocket(url, { headers: { Origin: ORIGIN, Cookie: this.cookie } });
		this.#socket = socket;
		return new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				this.close();
				reject(new Error(`${this.name}: no state within 10 s`));
			}, 10_000);
			socket.on('open', () => {
				socket.send(
					JSON.stringify({
						t: 'join',
						v: PROTOCOL_VERSION,
						d: { code, name: this.name, avatar: { emoji: '🍌', color: 'banana' } }
					})
				);
				this.#heartbeat = setInterval(() => this.send('ping'), 20_000);
			});
			socket.on('message', (data) => {
				const frame = JSON.parse(data.toString()) as {
					t: string;
					d?: { type?: string; code?: string };
				};
				if (frame.t === 'state') {
					this.state = frame.d as unknown as State;
					this.states.push(this.state);
					this.serverTime = this.state.serverTime;
					clearTimeout(timer);
					resolve();
				} else if (frame.t === 'pong') {
					this.serverTime = (frame.d as unknown as { serverTime: number }).serverTime;
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
				clearInterval(this.#heartbeat);
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
		clearInterval(this.#heartbeat);
		this.#socket?.close();
	}
}

/** Joins `count` bots, a few at a time. */
export async function joinBots(code: string, names: string[]): Promise<Bot[]> {
	const bots: Bot[] = [];
	for (const name of names) bots.push(await Bot.join(code, name));
	return bots;
}
