// Package integration holds the Phase 2 integration tests of the game server:
// they drive the real stack (room.Hub + store + httpapi.Server + ws.Handler on
// an httptest.Server, wired like cmd/server) with real WebSocket clients and
// an injected room.ManualClock. The package has no production code.
package integration
