import { ApiError, api, type Api } from '$lib/api';
import type { Lang, PublicRoom } from '$lib/protocol';

/** A single visible-page poller. Cancelled requests never publish results or reset cooldowns. */
export class RoomBrowser {
	rooms = $state<PublicRoom[]>([]);
	loading = $state(false);
	error = $state<string | null>(null);
	lang = $state<Lang | undefined>();
	#request: AbortController | null = null;
	#timer: ReturnType<typeof setTimeout> | undefined;
	#retryAt = 0;
	#doc: Document | null = null;
	#active = false;

	constructor(
		lang?: Lang,
		private client: Pick<Api, 'listRooms'> = api
	) {
		this.lang = lang;
	}

	start(doc: Document): () => void {
		this.#doc = doc;
		this.#active = true;
		doc.addEventListener('visibilitychange', this.#visibility);
		void this.refresh();
		return () => {
			this.#active = false;
			doc.removeEventListener('visibilitychange', this.#visibility);
			this.#cancel();
		};
	}

	#cancel() {
		clearTimeout(this.#timer);
		this.#request?.abort();
		this.#request = null;
		this.loading = false;
	}

	#visibility = () => {
		if (this.#doc?.visibilityState === 'hidden') this.#cancel();
		else void this.refresh();
	};

	filter(lang?: Lang) {
		if (lang === this.lang) return;
		this.#cancel();
		this.lang = lang;
		this.rooms = [];
		void this.refresh();
	}

	#schedule(delay: number) {
		clearTimeout(this.#timer);
		if (this.#active && this.#doc?.visibilityState !== 'hidden') {
			this.#timer = setTimeout(() => void this.refresh(), delay);
		}
	}

	async refresh() {
		if (!this.#active || this.#doc?.visibilityState === 'hidden' || this.#request) return;
		if (Date.now() < this.#retryAt) {
			this.#schedule(this.#retryAt - Date.now());
			return;
		}
		clearTimeout(this.#timer);
		const request = new AbortController();
		this.#request = request;
		this.loading = true;
		try {
			const result = await this.client.listRooms(this.lang, { signal: request.signal });
			if (request.signal.aborted) return;
			this.rooms = (result.rooms ?? []).slice(0, 50);
			this.error = null;
		} catch (error) {
			if (request.signal.aborted) return;
			this.error = error instanceof ApiError ? error.code : 'unknown';
			if (error instanceof ApiError && error.retryAfter !== null) {
				this.#retryAt = Date.now() + Math.max(0, error.retryAfter) * 1000;
			}
		} finally {
			if (this.#request === request) {
				this.#request = null;
				this.loading = false;
				this.#schedule(Math.max(15_000, this.#retryAt - Date.now()));
			}
		}
	}
}
