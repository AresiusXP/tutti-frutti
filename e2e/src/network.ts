import type { BrowserContext } from '@playwright/test';
import type { Guard } from './guard';

interface TrackedSockets {
	__tfSockets?: Set<WebSocket>;
}

/** How long the "network is away" allowances stay after `restore()` (see there). */
const SETTLE_MS = 2000;

/**
 * Network trouble on demand for one player. `context.setOffline(true)` blocks HTTP and fires the
 * `offline` event but leaves an established WebSocket alone, so "the connection is gone" also needs
 * the socket closed: every WebSocket the page opens is tracked (init script) and closed by `drop()`,
 * which is what the server sees when a phone loses its network: the player is marked offline.
 */
export class Network {
	#releases: (() => void)[] = [];

	constructor(
		private readonly context: BrowserContext,
		private readonly guard: Guard
	) {}

	async install(): Promise<void> {
		await this.context.addInitScript(() => {
			const w = window as unknown as TrackedSockets;
			const sockets = (w.__tfSockets = new Set<WebSocket>());
			const Native = window.WebSocket;
			function Tracked(this: unknown, url: string | URL, protocols?: string | string[]) {
				const socket = new Native(url, protocols);
				sockets.add(socket);
				socket.addEventListener('close', () => sockets.delete(socket));
				return socket;
			}
			Tracked.prototype = Native.prototype;
			Object.assign(Tracked, {
				CONNECTING: Native.CONNECTING,
				OPEN: Native.OPEN,
				CLOSING: Native.CLOSING,
				CLOSED: Native.CLOSED
			});
			window.WebSocket = Tracked as unknown as typeof WebSocket;
		});
	}

	/** Offline, and the room socket closed (the server sees the player leave; the app reconnects later). */
	async drop(): Promise<void> {
		// Allowed only while the network is away: `restore()` takes the allowances back.
		this.#releases.push(
			this.guard.allow({
				kind: 'failed',
				error: /ERR_INTERNET_DISCONNECTED|ERR_FAILED/,
				why: 'the test took the network away on purpose (ERR_FAILED: the service worker passing it on)'
			}),
			this.guard.allow({
				kind: 'console',
				text: /WebSocket connection to .* failed/,
				why: 'new sockets cannot open while the network is down'
			})
		);
		await this.context.setOffline(true);
		await Promise.all(
			this.context.pages().map((page) =>
				page.evaluate(() => {
					for (const socket of (window as unknown as TrackedSockets).__tfSockets ?? [])
						socket.close(1000);
				})
			)
		);
	}

	/**
	 * The network is back: the app notices (`online` event) and reconnects.
	 *
	 * The "network is away" allowances outlive this call by `SETTLE_MS`: the failure event of a
	 * request that died while offline (the app's retried `GET /api/session`, passed on by the
	 * service worker as ERR_FAILED) can reach the guard a moment after `setOffline(false)` resolves,
	 * because it comes from another CDP target, and it must still be judged by the allowance of the
	 * moment it failed. They only match the network-down error names, so a real failure after the
	 * network is back (a refused connection, an HTTP error) is still reported.
	 */
	async restore(): Promise<void> {
		await this.context.setOffline(false);
		const releases = this.#releases.splice(0);
		setTimeout(() => {
			for (const release of releases) release();
		}, SETTLE_MS).unref();
	}
}
