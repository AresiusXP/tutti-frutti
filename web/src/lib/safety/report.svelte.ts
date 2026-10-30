/**
 * One report being written. The idempotency key belongs to the draft: sending again after a failure
 * (the answer may have been lost on the way, the server may have been busy) uses the same key, so the
 * server ends up with exactly one report. Success is only ever claimed after the server answered 200/201.
 */
import { ApiError } from '$lib/api';
import type { SafetyApi } from './api';
import { safetyApi } from './api';
import {
	buildReport,
	newIdempotencyKey,
	type ReportContext,
	type ReportTarget,
	MAX_COMMENT
} from './targets';

export type ReportStatus = 'idle' | 'sending' | 'sent' | 'failed';

export interface ReportDeps {
	api?: SafetyApi;
	newKey?: () => string;
}

export class ReportDraft {
	reason = $state('');
	comment = $state('');
	status = $state<ReportStatus>('idle');
	/** Error code of the last failed send (`rate_limited`, `server_busy`, `network`, `not_in_room`, ...). */
	error = $state<string | null>(null);
	/** The player pressed Send without a reason. */
	missingReason = $state(false);

	readonly target: ReportTarget;
	readonly key: string;
	readonly #context: () => ReportContext;
	readonly #api: SafetyApi;

	constructor(target: ReportTarget, context: () => ReportContext, deps: ReportDeps = {}) {
		this.target = target;
		this.#context = context;
		this.#api = deps.api ?? safetyApi;
		this.key = (deps.newKey ?? newIdempotencyKey)();
	}

	get remaining(): number {
		return MAX_COMMENT - [...this.comment].length;
	}

	/** Sends (or re-sends) the report. Resolves `true` only when the server stored it. */
	async send(): Promise<boolean> {
		if (this.status === 'sending' || this.status === 'sent') return this.status === 'sent';
		if (!this.reason) {
			this.missingReason = true;
			return false;
		}
		this.missingReason = false;
		this.status = 'sending';
		this.error = null;
		try {
			await this.#api.report(
				buildReport(
					this.target,
					this.#context(),
					{ reason: this.reason, comment: this.comment },
					this.key
				)
			);
			this.status = 'sent';
			return true;
		} catch (e) {
			this.status = 'failed';
			this.error = e instanceof ApiError ? e.code : 'unknown';
			return false;
		}
	}
}
