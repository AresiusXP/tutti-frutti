import { expect, type BrowserContext, type Page } from '@playwright/test';
import { msg, msgPattern, type Locale, type Params } from './messages';
import type { Network } from './network';
import { ClosedPage } from './pages/closed';
import { GatePage } from './pages/gate';
import { HomePage, ROOM_URL, type AvatarChoice } from './pages/home';
import { CategoryPicker, LobbyPage } from './pages/lobby';
import { LetterPage, PlayingPage } from './pages/playing';
import { FinalPage, ReviewPage, SummaryPage } from './pages/review';

export type ScreenName = 'lobby' | 'letter' | 'playing' | 'review' | 'summary' | 'final';

export interface PlayerOptions {
	locale?: Locale;
	/**
	 * The browser's own language tag (`navigator.language`, `Accept-Language`) when it should not be the
	 * default one of `locale` (es-AR, en-US, nl-NL): the first-visit language detection specs.
	 */
	browserLanguage?: string;
	avatar?: AvatarChoice;
	/** Proxy the room WebSocket so the connection can be dropped on demand (`player.network`). */
	network?: boolean;
	/** Ask the browser for reduced motion (calmer screenshots and contrast checks). */
	reducedMotion?: boolean;
	/** Another screen than the project's phone (390x844), e.g. a small 360x740 one. */
	viewport?: { width: number; height: number };
	/**
	 * `'block'` keeps the service worker out: `page.route` cannot see the requests a controlled page makes
	 * through it, so a test that fakes `/api/*` (a signed-in account) needs this.
	 */
	serviceWorkers?: 'allow' | 'block';
}

/** One player = one browser context (own cookies/storage, so own guest id) with one tab. */
export class Player {
	readonly home = new HomePage(this);
	readonly gate = new GatePage(this);
	readonly lobby = new LobbyPage(this);
	readonly picker = new CategoryPicker(this);
	readonly letter = new LetterPage(this);
	readonly playing = new PlayingPage(this);
	readonly review = new ReviewPage(this);
	readonly summary = new SummaryPage(this);
	readonly final = new FinalPage(this);
	readonly closed = new ClosedPage(this);

	/** The `t` of every frame this player's first tab has sent over its room sockets, in order. */
	readonly #sent: string[] = [];

	constructor(
		readonly name: string,
		/** UI language; changes when the player presses ES / EN. */
		public locale: Locale,
		readonly context: BrowserContext,
		readonly page: Page,
		/** Present when the player was created with `{ network: true }`: drop / restore the connection. */
		readonly network?: Network
	) {
		page.on('websocket', (socket) =>
			socket.on('framesent', ({ payload }) => {
				try {
					const frame = JSON.parse(payload.toString()) as { t?: unknown };
					if (typeof frame.t === 'string') this.#sent.push(frame.t);
				} catch {
					// not a JSON frame: nothing the app sends
				}
			})
		);
	}

	/** How many frames of this type (`ask_stop`, `vote_stop`, `basta`, ...) the player has sent so far. */
	sentCount(type: string): number {
		return this.#sent.filter((t) => t === type).length;
	}

	t(key: string, params?: Params): string {
		return msg(this.locale, key, params);
	}

	/** The room code of the page the player is on (`/KT7P`). */
	get code(): string {
		const { pathname } = new URL(this.page.url());
		if (!ROOM_URL.test(pathname)) throw new Error(`${this.name} is not in a room: ${pathname}`);
		return pathname.slice(1);
	}

	/** Opens a second tab of the same player (same cookies, own sessionStorage). */
	async newTab(): Promise<Page> {
		return this.context.newPage();
	}

	/** Waits for the screen of a phase (each screen is a `<main>` with its own accessible name). */
	async expectScreen(screen: ScreenName, options: { timeout?: number } = {}): Promise<void> {
		const names: Record<ScreenName, string | RegExp> = {
			lobby: this.t('lobby_screen_label'),
			letter: this.t('play_letter_screen'),
			playing: msgPattern(this.locale, 'play_screen_label'),
			review: msgPattern(this.locale, 'review_label'),
			summary: msgPattern(this.locale, 'summary_label'),
			final: this.t('final_label')
		};
		const name = names[screen];
		await expect(
			this.page.getByRole('main', typeof name === 'string' ? { name, exact: true } : { name })
		).toBeVisible(options);
	}

	/** Everything the user can read (visible text) plus the accessible names of the page. */
	async readableText(): Promise<string> {
		const visible = await this.page.locator('body').innerText();
		const names = await this.page.locator('body').ariaSnapshot();
		return `${visible}\n${names}`;
	}

	/** "Estás sin conexión": the global banner while the browser reports itself offline. */
	get offlineBanner() {
		return this.page.getByRole('status').filter({ hasText: this.t('offline') });
	}

	/** "Reconectando…": the pill over the last screen while the socket to the room is down. */
	get reconnectingBanner() {
		return this.page.getByRole('status').filter({ hasText: this.t('net_reconnecting') });
	}

	/** Browser offline (HTTP fails, `offline` event fires) / back online. */
	async setOffline(offline: boolean): Promise<void> {
		await this.context.setOffline(offline);
	}

	async reload(): Promise<void> {
		await this.page.reload();
	}
}
