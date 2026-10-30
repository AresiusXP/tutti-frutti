/**
 * REST client (`/api`). Same-origin: the guest cookie travels with
 * `credentials: 'same-origin'` and the browser adds the `Origin` header the
 * server checks on unsafe methods. Failures are typed `ApiError`s.
 */
import type {
	CreateRoomRequest,
	CreateRoomResponse,
	Lang,
	PublicRoomsResponse,
	RoomInfo
} from '$lib/protocol';
export type { CreateRoomRequest, RoomInfo } from '$lib/protocol';

/** Codes the UI knows how to explain (see `$lib/errors`); anything else is `unknown`. */
export type ApiErrorCode =
	| 'room_not_found'
	| 'server_busy'
	| 'rate_limited'
	| 'invalid_name'
	| 'invalid_avatar'
	| 'invalid_lang'
	| 'forbidden_origin'
	| 'network'
	| 'unknown';

export class ApiError extends Error {
	/** HTTP status; 0 when there was no response (offline, DNS, connection reset). */
	readonly status: number;
	/** Stable snake_case code: the server's `{"error": code}` or one derived from the status. */
	readonly code: ApiErrorCode | (string & {});
	/** Seconds from `Retry-After`, when the server sent it. */
	readonly retryAfter: number | null;

	constructor(status: number, code: string, retryAfter: number | null = null) {
		super(`api ${status} ${code}`);
		this.name = 'ApiError';
		this.status = status;
		this.code = code;
		this.retryAfter = retryAfter;
	}

	/** True for failures that a retry can fix: no response, rate limit, or the server draining/restarting. */
	get retryable(): boolean {
		return this.status === 0 || this.status === 429 || this.status >= 500;
	}
}

export interface RequestOptions {
	signal?: AbortSignal;
}

export interface Api {
	/** Makes sure the guest cookie exists (`GET /api/session`). */
	ensureSession(opts?: RequestOptions): Promise<void>;
	getRoom(code: string, opts?: RequestOptions): Promise<RoomInfo>;
	createRoom(req: CreateRoomRequest, opts?: RequestOptions): Promise<CreateRoomResponse>;
	listRooms(lang?: Lang, opts?: RequestOptions): Promise<PublicRoomsResponse>;
}

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

const STATUS_CODES: Record<number, string> = {
	429: 'rate_limited',
	502: 'server_busy',
	503: 'server_busy',
	504: 'server_busy'
};

/** Builds the typed error of a non-2xx answer (shared with `$lib/safety/api`). */
export async function failure(res: Response): Promise<ApiError> {
	let code = '';
	try {
		const body: unknown = await res.json();
		const err = (body as { error?: unknown } | null)?.error;
		if (typeof err === 'string') code = err;
	} catch {
		// proxies answer with plain text/HTML (Traefik: "no available server")
	}
	// The status decides for rate limits and drain gaps, whatever a proxy put in the body.
	code = STATUS_CODES[res.status] ?? (code || 'unknown');
	const retryHeader = res.headers.get('Retry-After') ?? '';
	const retryAfter = /^\d+$/.test(retryHeader)
		? Number(retryHeader)
		: Math.max(0, Math.ceil((Date.parse(retryHeader) - Date.now()) / 1000));
	return new ApiError(res.status, code, Number.isFinite(retryAfter) ? retryAfter : null);
}

async function json<T>(res: Response): Promise<T> {
	try {
		return (await res.json()) as T;
	} catch {
		// e.g. the SPA fallback page answering where the API proxy is missing
		throw new ApiError(res.status, 'unknown');
	}
}

export function createApi(
	fetchFn: FetchLike = (input, init) => globalThis.fetch(input, init)
): Api {
	async function request(path: string, init: RequestInit = {}): Promise<Response> {
		let res: Response;
		try {
			res = await fetchFn(path, { credentials: 'same-origin', ...init });
		} catch (e) {
			if (init.signal?.aborted) throw e;
			throw new ApiError(0, 'network');
		}
		if (!res.ok) throw await failure(res);
		return res;
	}

	return {
		async ensureSession(opts = {}) {
			const res = await request('/api/session', { signal: opts.signal });
			// Only the Set-Cookie matters. Release the body so closing the room right
			// afterwards does not abort an already answered request (net::ERR_ABORTED).
			// Started, not awaited: the answer is already here and a caller must not
			// wait a stream tick for it.
			res.body?.cancel().catch(() => undefined);
		},
		async getRoom(code, opts = {}) {
			let res: Response;
			try {
				res = await request(`/api/rooms/${encodeURIComponent(code)}`, { signal: opts.signal });
			} catch (e) {
				if (e instanceof ApiError && e.status === 404) {
					throw new ApiError(404, 'room_not_found');
				}
				throw e;
			}
			return json<RoomInfo>(res);
		},
		async listRooms(lang, opts = {}) {
			const res = await request(`/api/rooms${lang ? `?lang=${encodeURIComponent(lang)}` : ''}`, {
				signal: opts.signal
			});
			return json<PublicRoomsResponse>(res);
		},
		async createRoom(req, opts = {}) {
			const res = await request('/api/rooms', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(req),
				signal: opts.signal
			});
			return json<CreateRoomResponse>(res);
		}
	};
}

export const api: Api = createApi();
