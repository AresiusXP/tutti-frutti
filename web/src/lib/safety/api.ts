/**
 * REST calls of the content-safety layer (docs/DESIGN.md, "Content safety"): terms
 * acceptance, reports and blocks. Same-origin like the rest of the app: the `tf_guest`
 * cookie travels with `credentials: 'same-origin'`. Failures are the shared typed
 * `ApiError` (status 0 = no response); answers are validated, never trusted.
 *
 * None of these needs the terms to be accepted (reporting and blocking must always work).
 */
import { ApiError, failure } from '$lib/api';
import type {
	AcceptTermsRequest,
	BlockedPlayer,
	BlockRequest,
	BlockResponse,
	ReportRequest,
	ReportResponse,
	TermsStatus
} from '$lib/protocol/api';

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface SafetyApi {
	/** `POST /api/terms/accept`: records the acceptance of `req.version` (idempotent). */
	acceptTerms(req: AcceptTermsRequest): Promise<TermsStatus>;
	/** `POST /api/reports`: `200`/`201` mean the report is stored; anything else is an `ApiError`. */
	report(req: ReportRequest): Promise<ReportResponse>;
	/** `GET /api/blocks`: the caller's own block list, newest first. */
	blocks(): Promise<BlockedPlayer[]>;
	/** `POST /api/blocks`: blocks a player and removes the caller from the live rooms they share. */
	block(req: BlockRequest): Promise<BlockResponse>;
	/** `DELETE /api/blocks/{playerId}` (204). */
	unblock(playerId: string): Promise<void>;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
	typeof value === 'object' && value !== null && !Array.isArray(value);

/** Reads the `terms` object of `GET /api/session` / `POST /api/terms/accept`; `null` when it is not well formed. */
export function parseTerms(value: unknown): TermsStatus | null {
	if (!isRecord(value)) return null;
	const { requiredVersion, acceptedVersion, acceptanceRequired } = value;
	if (typeof requiredVersion !== 'string' || typeof acceptanceRequired !== 'boolean') return null;
	if (acceptedVersion !== null && typeof acceptedVersion !== 'string') return null;
	return { requiredVersion, acceptedVersion, acceptanceRequired };
}

function parseBlocked(value: unknown): BlockedPlayer | null {
	if (!isRecord(value)) return null;
	const { playerId, name, blockedAt } = value;
	if (typeof playerId !== 'string' || playerId === '') return null;
	return {
		playerId,
		name: typeof name === 'string' ? name : '',
		blockedAt: typeof blockedAt === 'number' && Number.isFinite(blockedAt) ? blockedAt : 0
	};
}

export function createSafetyApi(
	fetchFn: FetchLike = (input, init) => globalThis.fetch(input, init)
): SafetyApi {
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

	const post = (path: string, payload: unknown) =>
		request(path, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(payload)
		});

	return {
		async acceptTerms(req) {
			const res = await post('/api/terms/accept', req);
			const terms = parseTerms(await body(res));
			if (!terms) throw new ApiError(res.status, 'unknown');
			return terms;
		},

		async report(req) {
			const res = await post('/api/reports', req);
			const data = await body(res);
			if (!isRecord(data) || typeof data.id !== 'string') throw new ApiError(res.status, 'unknown');
			return { id: data.id };
		},

		async blocks() {
			const res = await request('/api/blocks');
			const data = await body(res);
			if (!isRecord(data) || !Array.isArray(data.blocks)) throw new ApiError(res.status, 'unknown');
			return data.blocks.flatMap((entry) => {
				const block = parseBlocked(entry);
				return block ? [block] : [];
			});
		},

		async block(req) {
			const res = await post('/api/blocks', req);
			const data = await body(res);
			const block = isRecord(data) ? parseBlocked(data.block) : null;
			if (!isRecord(data) || !block) throw new ApiError(res.status, 'unknown');
			const left = Array.isArray(data.leftRooms)
				? data.leftRooms.filter((code): code is string => typeof code === 'string')
				: [];
			return { block, leftRooms: left };
		},

		async unblock(playerId) {
			await request(`/api/blocks/${encodeURIComponent(playerId)}`, { method: 'DELETE' });
		}
	};
}

export const safetyApi: SafetyApi = createSafetyApi();
