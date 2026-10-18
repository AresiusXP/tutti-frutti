import { expect, type BrowserContext, type Request } from '@playwright/test';
import { ORIGIN } from './env';

/**
 * The global guard: every test fails on
 *  - any uncaught page error,
 *  - any console `error`,
 *  - any failed or `>= 400` same-origin request,
 *  - any message that smells like a Content Security Policy violation (never allowable).
 * A test that provokes one on purpose says so with `guard.allow(...)` (and says why).
 */
export type Allowance =
	| { kind: 'response'; url: RegExp; status?: number; why: string }
	| { kind: 'failed'; url?: RegExp; error?: RegExp; why: string }
	| { kind: 'console'; text: RegExp; why: string };

const CSP = /content security policy|refused to |violates the following/i;

function sameOrigin(url: string): boolean {
	try {
		return new URL(url).origin === ORIGIN;
	} catch {
		return false;
	}
}

export class Guard {
	readonly problems: string[] = [];
	readonly #allowances = new Set<Allowance>();
	readonly #pending = new Set<Promise<void>>();

	/** Allows the described noise until the returned function is called. */
	allow(rule: Allowance): () => void {
		this.#allowances.add(rule);
		return () => void this.#allowances.delete(rule);
	}

	#allowed(match: (rule: Allowance) => boolean): boolean {
		for (const rule of this.#allowances) if (match(rule)) return true;
		return false;
	}

	#report(label: string, text: string) {
		this.problems.push(`[${label}] ${text}`);
	}

	attach(context: BrowserContext, label: string) {
		context.on('weberror', (webError) => {
			const error = webError.error();
			this.#report(label, `pageerror: ${error.message}`);
		});
		context.on('page', (page) => {
			page.on('crash', () => this.#report(label, 'the page crashed'));
		});
		context.on('console', (message) => {
			const text = message.text();
			if (CSP.test(text)) {
				this.#report(label, `CSP violation: ${text.slice(0, 300)}`);
				return;
			}
			if (message.type() !== 'error') return;
			const { url } = message.location();
			// "Failed to load resource": the request/response listeners below judge it
			if (/^Failed to load resource/.test(text) && sameOrigin(url)) return;
			if (this.#allowed((r) => r.kind === 'console' && r.text.test(text))) return;
			this.#report(label, `console.error: ${text.slice(0, 300)}`);
		});
		context.on('requestfailed', (request) => {
			const url = request.url();
			if (!sameOrigin(url)) return;
			const error = request.failure()?.errorText ?? 'failed';
			// judged now, while the allowances of that moment apply
			const allowed = this.#allowed(
				(r) =>
					r.kind === 'failed' && (!r.url || r.url.test(url)) && (!r.error || r.error.test(error))
			);
			this.#track(this.#judgeFailure(label, request, error, allowed));
		});
		context.on('response', (response) => {
			const url = response.url();
			const status = response.status();
			if (status < 400 || !sameOrigin(url)) return;
			if (
				this.#allowed(
					(r) =>
						r.kind === 'response' &&
						r.url.test(url) &&
						(r.status === undefined || r.status === status)
				)
			) {
				return;
			}
			this.#report(label, `HTTP ${status}: ${response.request().method()} ${url}`);
		});
	}

	async #judgeFailure(
		label: string,
		request: Request,
		error: string,
		allowed: boolean
	): Promise<void> {
		if (allowed) return;
		// The app does not read the body of `GET /api/session` (it only needs the cookie), so
		// closing the room aborts that already answered request: not a failed request.
		if (/ERR_ABORTED/.test(error)) {
			const response = await request.response().catch(() => null);
			if (response && response.status() < 400) return;
		}
		this.#report(label, `request failed: ${request.method()} ${request.url()} (${error})`);
	}

	#track(work: Promise<void>) {
		this.#pending.add(work);
		void work.finally(() => this.#pending.delete(work));
	}

	async assertClean() {
		await Promise.allSettled([...this.#pending]);
		expect(
			this.problems,
			'browser problems (page errors, console errors, failed requests, CSP)'
		).toEqual([]);
	}
}
