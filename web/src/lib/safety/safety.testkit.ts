/** Test doubles for the safety layer: terms fixtures and a scriptable `SafetyApi`. */
import { vi } from 'vitest';
import { ApiError } from '$lib/api';
import type {
	AcceptTermsRequest,
	BlockedPlayer,
	BlockRequest,
	BlockResponse,
	ReportRequest,
	ReportResponse,
	TermsStatus
} from '$lib/protocol/api';
import type { SafetyApi } from './api';

export const TERMS_OK: TermsStatus = {
	requiredVersion: '2026-10',
	acceptedVersion: '2026-10',
	acceptanceRequired: false
};
export const TERMS_DUE: TermsStatus = {
	requiredVersion: '2026-10',
	acceptedVersion: null,
	acceptanceRequired: true
};

export function deferred<T>() {
	let resolve!: (v: T) => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

type Step<T> = T | Error | Promise<T>;

/**
 * A `SafetyApi` that records every call and answers from a script: set `reportAnswers` /
 * `acceptAnswers` to a list consumed in order (the last one repeats), or override a method.
 */
export class FakeSafetyApi implements SafetyApi {
	reports: ReportRequest[] = [];
	accepts: AcceptTermsRequest[] = [];
	blocked: BlockRequest[] = [];
	unblocked: string[] = [];
	listCalls = 0;

	list: BlockedPlayer[] = [];
	reportAnswers: Array<Step<ReportResponse>> = [{ id: 'report-1' }];
	acceptAnswers: Array<Step<TermsStatus>> = [TERMS_OK];
	blockAnswer: Step<BlockResponse> = {
		block: { playerId: 'p2', name: 'Beto', blockedAt: 1 },
		leftRooms: []
	};
	listAnswer: Step<BlockedPlayer[]> | null = null;
	unblockAnswer: Step<void> = undefined;

	#next<T>(steps: Array<Step<T>>, used: number): Promise<T> {
		const step = steps[Math.min(used, steps.length - 1)];
		return step instanceof Error ? Promise.reject(step) : Promise.resolve(step);
	}

	acceptTerms = vi.fn((req: AcceptTermsRequest) => {
		this.accepts.push(req);
		return this.#next(this.acceptAnswers, this.accepts.length - 1);
	});

	report = vi.fn((req: ReportRequest) => {
		this.reports.push(req);
		return this.#next(this.reportAnswers, this.reports.length - 1);
	});

	blocks = vi.fn(() => {
		this.listCalls++;
		const answer = this.listAnswer ?? [...this.list];
		return answer instanceof Error ? Promise.reject(answer) : Promise.resolve(answer);
	});

	block = vi.fn((req: BlockRequest) => {
		this.blocked.push(req);
		return this.blockAnswer instanceof Error
			? Promise.reject(this.blockAnswer)
			: Promise.resolve(this.blockAnswer);
	});

	unblock = vi.fn((playerId: string) => {
		this.unblocked.push(playerId);
		return this.unblockAnswer instanceof Error
			? Promise.reject(this.unblockAnswer)
			: Promise.resolve(this.unblockAnswer);
	});
}

export const apiError = (status: number, code: string, retryAfter: number | null = null) =>
	new ApiError(status, code, retryAfter);
