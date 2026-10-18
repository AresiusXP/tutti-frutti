import { fileURLToPath } from 'node:url';
import path from 'node:path';

/** Where the app under test lives (the compose edge listens on :8000 by default). */
export const BASE_URL = (process.env.E2E_BASE_URL ?? 'http://localhost:8000').replace(/\/+$/, '');

export const ORIGIN = new URL(BASE_URL).origin;

/**
 * Compose project of the stack under test. Only the tests that restart the server need it:
 * without it they are skipped (e.g. when the suite runs against a remote deployment).
 */
export const COMPOSE_PROJECT = process.env.E2E_COMPOSE_PROJECT ?? '';

/** The web app sources: the message files are read from here so labels can never drift from the app. */
export const WEB_DIR = path.resolve(
	process.env.E2E_WEB_DIR ?? path.join(path.dirname(fileURLToPath(import.meta.url)), '../../web')
);

/** Parallel workers: rooms are independent, but the server rate limits are shared global buckets. */
export const WORKERS = Math.max(1, Number(process.env.E2E_WORKERS ?? 2) || 2);
