import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { BASE_URL, COMPOSE_PROJECT } from './env';
import type { Guard } from './guard';

const run = promisify(execFile);

/** `docker compose -p <project> restart server` — the pod restart of a deploy, minus Kubernetes. */
export async function restartServer(): Promise<void> {
	if (!COMPOSE_PROJECT) throw new Error('E2E_COMPOSE_PROJECT is not set');
	await run('docker', ['compose', '-p', COMPOSE_PROJECT, 'restart', 'server'], {
		timeout: 120_000
	});
}

const WORKER = '/usr/share/nginx/html/sw.js';
const WORKER_BACKUP = '/tmp/sw.js.e2e-original';

/** A command inside the web container, as root (the image's files belong to root). */
async function inWeb(script: string): Promise<void> {
	if (!COMPOSE_PROJECT) throw new Error('E2E_COMPOSE_PROJECT is not set');
	await run(
		'docker',
		['compose', '-p', COMPOSE_PROJECT, 'exec', '-T', '-u', '0', 'web', 'sh', '-c', script],
		{
			timeout: 60_000
		}
	);
}

/**
 * A web deploy, minus the image: the served `sw.js` gets one more line, so the browser sees a new worker
 * version (same precache, same shell). `revertWorker()` puts the original back.
 */
export async function deployNewWorker(): Promise<void> {
	await inWeb(
		`cp ${WORKER} ${WORKER_BACKUP} && printf '\\n// e2e: a newer version\\n' >> ${WORKER}`
	);
}

export async function revertWorker(): Promise<void> {
	await inWeb(
		`[ ! -f ${WORKER_BACKUP} ] || (cp ${WORKER_BACKUP} ${WORKER} && rm ${WORKER_BACKUP})`
	);
}

/** Polls until the API answers through the edge again. */
export async function waitForApi(timeoutMs = 60_000): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		try {
			const response = await fetch(`${BASE_URL}/api/session`);
			if (response.ok) return;
		} catch {
			// not up yet
		}
		if (Date.now() > deadline) throw new Error('the API did not come back');
		await new Promise((resolve) => setTimeout(resolve, 250));
	}
}

/**
 * While the server restarts the edge answers 502/503 and sockets fail to open: expected noise.
 * Returns the function that ends the allowance (call it once everybody is back).
 */
export function allowServerOutage(guard: Guard): () => void {
	const why = 'the server container is being restarted on purpose';
	const releases = [
		guard.allow({ kind: 'response', url: /\/api\//, why }),
		guard.allow({ kind: 'failed', url: /\/api\//, why }),
		guard.allow({ kind: 'console', text: /WebSocket connection to .* failed/, why })
	];
	return () => releases.forEach((release) => release());
}
