/**
 * REST calls of the account feature. Same-origin: the `tf_session` cookie
 * travels with `credentials: 'same-origin'` and the browser adds the `Origin`
 * header the server checks on unsafe methods. Failures are the shared typed
 * `ApiError` (status 0 = no response); responses are validated, never trusted.
 */
import { ApiError } from '$lib/api';
import type { Me, SavedProfile, SessionInfo, Stats } from './types';

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface AccountApi {
	session(): Promise<SessionInfo>;
	me(): Promise<Me>;
	/** `keepalive` lets the request outlive the page (flush on `pagehide`). */
	saveProfile(profile: SavedProfile, opts?: { keepalive?: boolean }): Promise<void>;
	stats(): Promise<Stats>;
	deleteMe(): Promise<void>;
	logout(): Promise<void>;
}

const STATUS_CODES: Record<number, string> = {
	401: 'unauthorized',
	429: 'rate_limited',
	502: 'server_busy',
	503: 'server_busy',
	504: 'server_busy'
};

async function failure(res: Response): Promise<ApiError> {
	let code = '';
	try {
		const body: unknown = await res.json();
		const err = (body as { error?: unknown } | null)?.error;
		if (typeof err === 'string') code = err;
	} catch {
		// proxies answer with plain text/HTML (Traefik: "no available server")
	}
	// The status decides for auth, rate limits and drain gaps, whatever a proxy put in the body.
	code = STATUS_CODES[res.status] ?? (code || 'unknown');
	const retryAfter = Number.parseInt(res.headers.get('Retry-After') ?? '', 10);
	return new ApiError(res.status, code, Number.isFinite(retryAfter) ? retryAfter : null);
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
	typeof value === 'object' && value !== null && !Array.isArray(value);

const count = (value: unknown): value is number =>
	typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

function parseProfile(value: unknown): SavedProfile | null | undefined {
	if (value === null) return null;
	if (!isRecord(value) || typeof value.nickname !== 'string' || !isRecord(value.avatar)) {
		return undefined;
	}
	const { emoji, color } = value.avatar;
	if (typeof emoji !== 'string' || typeof color !== 'string') return undefined;
	return { nickname: value.nickname, avatar: { emoji, color } };
}

export function createAccountApi(
	fetchFn: FetchLike = (input, init) => globalThis.fetch(input, init)
): AccountApi {
	async function request(path: string, init: RequestInit = {}): Promise<Response> {
		let res: Response;
		try {
			res = await fetchFn(path, { credentials: 'same-origin', ...init });
		} catch {
			throw new ApiError(0, 'network');
		}
		if (!res.ok) throw await failure(res);
		return res;
	}

	async function body(res: Response): Promise<unknown> {
		try {
			return await res.json();
		} catch {
			// e.g. the SPA fallback page answering where the API proxy is missing
			throw new ApiError(res.status, 'unknown');
		}
	}

	return {
		async session() {
			const res = await request('/api/session');
			const data = await body(res);
			if (!isRecord(data)) throw new ApiError(res.status, 'unknown');
			// A server without accounts (Phase 3) only sends `guest`: both flags are then false.
			return {
				guest: data.guest !== false,
				googleEnabled: data.googleEnabled === true,
				loggedIn: data.loggedIn === true
			};
		},

		async me() {
			const res = await request('/api/me');
			const data = await body(res);
			const profile = isRecord(data) ? parseProfile(data.profile) : undefined;
			if (!isRecord(data) || typeof data.id !== 'string' || typeof data.name !== 'string') {
				throw new ApiError(res.status, 'unknown');
			}
			if (profile === undefined) throw new ApiError(res.status, 'unknown');
			return { id: data.id, name: data.name, profile };
		},

		async saveProfile(profile, opts = {}) {
			await request('/api/me/profile', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(profile),
				...(opts.keepalive ? { keepalive: true } : {})
			});
		},

		async stats() {
			const res = await request('/api/me/stats');
			const data = await body(res);
			if (
				!isRecord(data) ||
				!count(data.gamesPlayed) ||
				!count(data.wins) ||
				!count(data.totalPoints) ||
				!count(data.bestRound) ||
				!count(data.uniqueAnswers)
			) {
				throw new ApiError(res.status, 'unknown');
			}
			return {
				gamesPlayed: data.gamesPlayed,
				wins: data.wins,
				totalPoints: data.totalPoints,
				bestRound: data.bestRound,
				uniqueAnswers: data.uniqueAnswers
			};
		},

		async deleteMe() {
			await request('/api/me', { method: 'DELETE' });
		},

		async logout() {
			await request('/api/auth/logout', { method: 'POST' });
		}
	};
}
