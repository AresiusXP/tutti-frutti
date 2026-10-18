/**
 * Thin, injectable pieces of the WebSocket transport: the minimal socket the
 * session needs (so tests can fake it), URL building and frame encoding.
 * The wire format lives in `$lib/protocol` (generated from Go).
 */
import { Version, type Envelope } from '$lib/protocol';

/** `WebSocket.OPEN`, spelled out so Node (tests) does not need the DOM global. */
export const WS_OPEN = 1;

/** The subset of `WebSocket` that `WsRoomSession` uses. */
export interface SocketLike {
	readonly readyState: number;
	onopen: ((ev: unknown) => void) | null;
	onmessage: ((ev: { data: unknown }) => void) | null;
	onclose: ((ev: { code: number }) => void) | null;
	onerror: ((ev: unknown) => void) | null;
	send(data: string): void;
	close(code?: number, reason?: string): void;
}

export type SocketFactory = (url: string) => SocketLike;

export const browserSocket: SocketFactory = (url) => new WebSocket(url) as unknown as SocketLike;

/** `wss://host/ws` on https pages, `ws://host/ws` otherwise (dev on http://localhost). */
export function wsUrl(loc: { protocol: string; host: string }): string {
	return `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}/ws`;
}

/** One JSON text frame `{t, v, d?}`; `d` is omitted for empty payloads. */
export function encodeFrame(t: string, d?: unknown): string {
	const frame: Envelope = d === undefined ? { t, v: Version } : { t, v: Version, d };
	return JSON.stringify(frame);
}

/** Parses an incoming frame; anything that is not an envelope with a string `t` yields null. */
export function decodeFrame(raw: unknown): Envelope | null {
	if (typeof raw !== 'string') return null;
	try {
		const value: unknown = JSON.parse(raw);
		if (typeof value !== 'object' || value === null) return null;
		const env = value as Partial<Envelope>;
		return typeof env.t === 'string' ? (env as Envelope) : null;
	} catch {
		return null;
	}
}
