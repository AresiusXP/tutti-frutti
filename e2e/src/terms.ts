import type { APIRequestContext } from '@playwright/test';
import type { TermsStatus } from '../../web/src/lib/protocol/api';
import { BASE_URL, ORIGIN } from './env';
import type { Locale } from './messages';

/**
 * Every guest has to accept the terms before it may create or join a room (docs/DESIGN.md, "Content
 * safety"): without it `POST /api/rooms` and the WebSocket `join` answer `terms_required`. The helpers
 * that play without the terms screen (bots, REST room creation) and the players of every spec that is
 * not about the terms accept them the way the web app does: `GET /api/session` (mints the guest cookie
 * and says which version is required), then `POST /api/terms/accept` with the SAME cookie jar.
 * Acceptance is idempotent on the server; this module just does not ask twice for the same identity.
 */
const accepted = new WeakSet<object>();
const acceptedCookies = new Set<string>();

interface SessionBody {
	terms: TermsStatus;
}

function acceptBody(terms: TermsStatus, locale: Locale) {
	return { version: terms.requiredVersion, locale, age13Confirmed: true };
}

/** For a browser context's `request` (it shares the context's cookies): the identity of that player. */
export async function acceptTerms(
	request: APIRequestContext,
	locale: Locale = 'es'
): Promise<void> {
	if (accepted.has(request)) return;
	const session = await request.get('/api/session');
	if (!session.ok()) throw new Error(`GET /api/session answered ${session.status()}`);
	const { terms } = (await session.json()) as SessionBody;
	if (terms.acceptanceRequired) {
		const response = await request.post('/api/terms/accept', {
			headers: { Origin: ORIGIN },
			data: acceptBody(terms, locale)
		});
		if (!response.ok()) {
			throw new Error(
				`POST /api/terms/accept answered ${response.status()}: ${await response.text()}`
			);
		}
	}
	accepted.add(request);
}

/** For a bare `tf_guest=<value>` cookie (the WebSocket bots, which have no browser context). */
export async function acceptTermsWithCookie(cookie: string, locale: Locale = 'es'): Promise<void> {
	if (acceptedCookies.has(cookie)) return;
	const headers = { Cookie: cookie, Origin: ORIGIN };
	const session = await fetch(`${BASE_URL}/api/session`, { headers });
	if (!session.ok) throw new Error(`GET /api/session answered ${session.status}`);
	const { terms } = (await session.json()) as SessionBody;
	if (terms.acceptanceRequired) {
		const response = await fetch(`${BASE_URL}/api/terms/accept`, {
			method: 'POST',
			headers: { ...headers, 'Content-Type': 'application/json' },
			body: JSON.stringify(acceptBody(terms, locale))
		});
		if (!response.ok) {
			throw new Error(
				`POST /api/terms/accept answered ${response.status}: ${await response.text()}`
			);
		}
	}
	acceptedCookies.add(cookie);
}
