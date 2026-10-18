/**
 * Wire types of the Phase 4 account REST API (`/api/session`, `/api/me`,
 * `/api/me/profile`, `/api/me/stats`). They are GENERATED from the Go structs
 * (`go tool tygo generate` → `$lib/protocol/api.ts`, never edited by hand); the
 * names below are the ones the account feature uses.
 */
import type { Avatar } from '$lib/protocol';
import type { Profile, SessionInfo, Stats, UserInfo } from '$lib/protocol/api';

export type { SessionInfo, Stats };

/** Emoji + colour as stored by the server (any short strings; the UI only offers its own palette). */
export type WireAvatar = Avatar;

/** What the user saved (`PUT /api/me/profile` body, `GET /api/me` `profile`). */
export type SavedProfile = Profile;

/** `GET /api/me`: `name` is the Google display name (≤ 16 runes, may be empty), `profile` is `null` until the first save. */
export type Me = UserInfo;

/** Result of an account action: errors are values, the UI never has to catch. */
export type Outcome = { ok: true } | { ok: false; code: string };

export const OK: Outcome = { ok: true };
