import { describe, expect, it } from 'vitest';
import { Version } from '$lib/protocol';
import { decodeFrame, encodeFrame, wsUrl } from './transport';

describe('wsUrl', () => {
	it('uses wss: on https and ws: otherwise, always /ws on the page host', () => {
		expect(wsUrl({ protocol: 'https:', host: 'tuttifrutti.aresius.xyz' })).toBe(
			'wss://tuttifrutti.aresius.xyz/ws'
		);
		expect(wsUrl({ protocol: 'http:', host: 'localhost:5173' })).toBe('ws://localhost:5173/ws');
	});
});

describe('frames', () => {
	it('encodes {t, v, d} and omits d for empty payloads', () => {
		expect(JSON.parse(encodeFrame('kick', { playerId: 'p2' }))).toEqual({
			t: 'kick',
			v: Version,
			d: { playerId: 'p2' }
		});
		expect(Version).toBe(2);
		expect(encodeFrame('ping')).toBe(`{"t":"ping","v":${Version}}`);
	});

	it('decodes envelopes and rejects everything else', () => {
		expect(decodeFrame('{"t":"state","v":1,"d":{"a":1}}')).toEqual({
			t: 'state',
			v: 1,
			d: { a: 1 }
		});
		expect(decodeFrame('{"t":"pong","v":1}')).toEqual({ t: 'pong', v: 1 });
		for (const bad of [
			'',
			'nope',
			'[]',
			'null',
			'42',
			'{"v":1}',
			'{"t":3}',
			42,
			null,
			undefined,
			{}
		]) {
			expect(decodeFrame(bad), String(bad)).toBeNull();
		}
	});
});
