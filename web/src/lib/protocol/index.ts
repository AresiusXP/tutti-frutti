// Entry point for the wire types shared with the Go server.
//
// catalog.ts, game.ts and protocol.ts are generated from the Go structs with
// `cd server && go tool tygo generate` (config: server/tygo.yaml) and must not
// be edited by hand; CI fails when they are stale. This file is hand-written.
export * from './catalog';
export * from './game';
export * from './protocol';
