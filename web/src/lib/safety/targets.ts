/**
 * What a report (or a block) is about, as the player sees it, and the wire body that follows from it.
 * The server resolves every reference against the room and never trusts it as a target
 * (docs/DESIGN.md, "Content safety": Reports), so nothing here needs more than the vocabulary of the room:
 * player ids, a round number, a category index and the label the reporter saw.
 */
import {
	ReportKindAIVerdict,
	ReportKindAnswer,
	ReportKindCustomLabel,
	ReportKindPlayer,
	ReportReasonHarassment,
	ReportReasonHate,
	ReportReasonImpersonation,
	ReportReasonOther,
	ReportReasonSelfHarm,
	ReportReasonSexual,
	ReportReasonSpam,
	ReportReasonViolence,
	type ReportRequest
} from '$lib/protocol/api';

export const MAX_COMMENT = 280;
export const MAX_OBSERVED = 200;

/** The fixed reasons, in the order they are shown (the server accepts exactly these). */
export const REPORT_REASONS = [
	ReportReasonHarassment,
	ReportReasonHate,
	ReportReasonSexual,
	ReportReasonViolence,
	ReportReasonSelfHarm,
	ReportReasonSpam,
	ReportReasonImpersonation,
	ReportReasonOther
] as const;
export type ReportReason = (typeof REPORT_REASONS)[number];

export interface Person {
	id: string;
	name: string;
}

/** An answer or an AI note the viewer is looking at: round number (1-based), category index and the text shown. */
export interface Shown {
	round: number;
	category: number;
	text: string;
}

export type ReportTarget =
	| { kind: typeof ReportKindPlayer; person: Person }
	| { kind: typeof ReportKindCustomLabel; category: number; label: string }
	| ({ kind: typeof ReportKindAnswer; person: Person } & Shown)
	| ({ kind: typeof ReportKindAIVerdict; person: Person } & Shown);

/** Where the room is, from the viewer's `State`: both are sent so the server can tell a reused code. */
export interface ReportContext {
	code: string;
	roomInstanceId: string | null;
}

/** Automated seats are `bot:<id>` (server `game.automatedIDPrefix`); the client only needs the prefix. */
export const isBotId = (id: string): boolean => id.startsWith('bot:');

/** The viewer's own seat and the automated ones are never reported or blocked. */
export const canTarget = (youId: string, id: string): boolean =>
	id !== '' && id !== youId && !isBotId(id);

/** At most `max` characters (code points, like the server's runes). */
export function clip(text: string, max: number): string {
	const chars = [...text];
	return chars.length <= max ? text : chars.slice(0, max).join('');
}

/** `POST /api/reports` body for a draft. `observedText` is what the reporter saw: the server only keeps it when the room is gone. */
export function buildReport(
	target: ReportTarget,
	ctx: ReportContext,
	form: { reason: string; comment: string },
	idempotencyKey: string
): ReportRequest {
	const comment = form.comment.trim();
	const base = {
		roomInstanceId: ctx.roomInstanceId || undefined,
		code: ctx.code,
		reason: form.reason,
		...(comment ? { comment: clip(comment, MAX_COMMENT) } : {}),
		idempotencyKey
	};
	switch (target.kind) {
		case ReportKindPlayer:
			return {
				...base,
				kind: target.kind,
				ref: { playerId: target.person.id },
				observedText: clip(target.person.name, MAX_OBSERVED)
			};
		case ReportKindCustomLabel:
			return {
				...base,
				kind: target.kind,
				ref: { category: target.category, label: target.label },
				observedText: clip(target.label, MAX_OBSERVED)
			};
		case ReportKindAnswer:
		case ReportKindAIVerdict:
			return {
				...base,
				kind: target.kind,
				ref: { playerId: target.person.id, round: target.round, category: target.category },
				observedText: clip(target.text, MAX_OBSERVED)
			};
	}
}

const KEY_ALPHABET = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';

/** 8 to 64 letters, digits, `-` and `_`: a UUID where the browser has one, else random characters. */
export function newIdempotencyKey(): string {
	const c = globalThis.crypto;
	if (typeof c?.randomUUID === 'function') return c.randomUUID();
	const bytes = new Uint8Array(24);
	if (typeof c?.getRandomValues === 'function') c.getRandomValues(bytes);
	else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
	return Array.from(bytes, (b) => KEY_ALPHABET[b % KEY_ALPHABET.length]).join('');
}
