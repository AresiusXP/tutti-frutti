import {
	test as base,
	expect,
	type Browser,
	type BrowserContext,
	type TestInfo
} from '@playwright/test';
import { Guard } from './guard';
import { LOCALE_TAGS } from './messages';
import { Network } from './network';
import { Player, type PlayerOptions } from './player';

/**
 * The server limits room creation to 30/min with a burst of 10, as ONE global bucket (in the compose
 * stack, like in the cluster, every client has the same address). Each worker mirrors its share of that
 * bucket and waits before a creation that would overdraw it, so the suite never meets a 429.
 */
const BURST = 8;
const REFILL_MS = 2200;

class RoomCreationPacer {
	#tokens: number;
	#last = Date.now();

	constructor(
		private readonly capacity: number,
		private readonly refillMs: number
	) {
		this.#tokens = capacity;
	}

	async take(): Promise<void> {
		for (;;) {
			const now = Date.now();
			this.#tokens = Math.min(this.capacity, this.#tokens + (now - this.#last) / this.refillMs);
			this.#last = now;
			if (this.#tokens >= 1) {
				this.#tokens -= 1;
				return;
			}
			await new Promise((resolve) =>
				setTimeout(resolve, Math.ceil((1 - this.#tokens) * this.refillMs))
			);
		}
	}
}

let pacer: RoomCreationPacer | undefined;

/** The browsers of one test, the shared guard and the room-creation pacing. */
export class App {
	readonly guard = new Guard();
	readonly players: Player[] = [];
	readonly #contexts: BrowserContext[] = [];
	readonly #disposers: (() => void | Promise<void>)[] = [];

	constructor(
		private readonly browser: Browser,
		readonly testInfo: TestInfo
	) {
		const workers = Math.max(1, testInfo.config.workers);
		pacer ??= new RoomCreationPacer(Math.max(1, Math.floor(BURST / workers)), REFILL_MS * workers);
	}

	/** A new player: own browser context (fresh storage and cookies, phone emulation of the project). */
	async player(name: string, options: PlayerOptions = {}): Promise<Player> {
		const locale = options.locale ?? 'es';
		const context = await this.browser.newContext({
			locale: options.browserLanguage ?? LOCALE_TAGS[locale],
			timezoneId: options.timezoneId ?? 'America/Argentina/Buenos_Aires',
			permissions: [],
			...(options.viewport ? { viewport: options.viewport } : {}),
			...(options.reducedMotion ? { reducedMotion: 'reduce' as const } : {}),
			...(options.serviceWorkers ? { serviceWorkers: options.serviceWorkers } : {})
		});
		this.#contexts.push(context);
		this.guard.attach(context, name);
		const network = options.network ? new Network(context, this.guard) : undefined;
		await network?.install();
		const page = await context.newPage();
		const player = new Player(name, locale, context, page, network);
		this.players.push(player);
		return player;
	}

	/** Call right before anything that creates a room. */
	async beforeRoomCreation(): Promise<void> {
		await pacer?.take();
	}

	/** Something to clean up when the test ends (bots, ...). */
	onDispose(cleanup: () => void | Promise<void>): void {
		this.#disposers.push(cleanup);
	}

	async dispose(): Promise<void> {
		await Promise.all(
			this.#disposers.map((cleanup) => Promise.resolve(cleanup()).catch(() => undefined))
		);
		await Promise.all(this.#contexts.map((context) => context.close().catch(() => undefined)));
	}
}

export const test = base.extend<{ app: App }>({
	app: async ({ browser }, use, testInfo) => {
		const app = new App(browser, testInfo);
		try {
			await use(app);
			await app.guard.assertClean();
		} finally {
			await app.dispose();
		}
	}
});

export { expect };
