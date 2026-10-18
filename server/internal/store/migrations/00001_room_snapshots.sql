-- +goose Up
CREATE TABLE room_snapshots (
  code        text PRIMARY KEY,
  state       jsonb NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX room_snapshots_updated_at_idx ON room_snapshots (updated_at);

-- +goose Down
DROP INDEX room_snapshots_updated_at_idx;
DROP TABLE room_snapshots;
