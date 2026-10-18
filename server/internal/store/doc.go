// Package store persists room snapshots so that active games survive a
// server restart, and the accounts of signed-in users with their lifetime
// statistics. The snapshot payload is opaque to the store: the room layer owns
// the JSON schema. Implementations: Memory (tests, local dev without a
// database) and Postgres; both implement [Backend], that is [Store] (snapshots)
// and [Accounts] (users, profile, game results).
//
// # Implementations
//
// [Memory] keeps snapshots in a mutex-guarded map. Nothing survives a restart;
// it backs unit tests and local runs with an empty DATABASE_URL.
//
// [Postgres] persists them in the room_snapshots table (code, state jsonb,
// updated_at timestamptz) through a pgx connection pool. NewPostgres applies
// the embedded goose migrations (migrations/*.sql) at startup, serialised across
// concurrent starters by a Postgres session advisory lock, and never writes the
// database URL or password into logs or errors. Migration logs go to the
// *slog.Logger given with [WithLogger]; without it the store is silent.
//
// # Behaviour shared by all implementations
//
// Both implementations validate Save identically: the code must be non-empty
// text and the state valid JSON without NUL characters (what jsonb can hold);
// otherwise Save fails with an error wrapping [ErrInvalidSnapshot]. Snapshots
// are stored and returned as copies, UpdatedAt is normalised to UTC with
// microsecond precision (the resolution of timestamptz) and LoadAll sorts by
// Code in byte order. Postgres stores State as jsonb, so the returned document is
// semantically equal to the saved one but may be re-formatted (whitespace, key
// order). Every method returns the context error when ctx is already done.
//
// # Accounts
//
// [Accounts] keeps users (the Google subject, the display name, an optional saved
// profile; no email, no tokens), their statistics and one row per (user, game)
// that makes [Accounts.RecordGame] idempotent: the row is inserted ON CONFLICT DO
// NOTHING and the statistics are only updated when it was inserted, in one
// transaction (Postgres) or under one lock (Memory). Users that no longer exist
// are skipped. Deleting a user removes everything that hangs off it (ON DELETE
// CASCADE). Both implementations validate their arguments identically
// ([ErrInvalidAccount], [ErrInvalidGame]) and report a missing user as
// [ErrUserNotFound]. User and game ids are lower-case UUIDs ([NewID], [ValidID]).
//
// # Testing
//
// The tests in this package run a common contract suite against Memory always
// and against Postgres only when TEST_DATABASE_URL is set (snapshots and
// accounts alike). Those tests TRUNCATE the data tables and drop the schema, so
// point them at a throwaway database,
// for example:
//
//	docker run -d --rm --name tf-store-test -e POSTGRES_USER=tutti \
//	  -e POSTGRES_PASSWORD=tutti -e POSTGRES_DB=tutti -p 127.0.0.1:55432:5432 postgres:18-alpine
//	TEST_DATABASE_URL='postgres://tutti:tutti@127.0.0.1:55432/tutti?sslmode=disable' \
//	  go test -race -cover ./internal/store/...
package store
