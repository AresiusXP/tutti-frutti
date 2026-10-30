-- +goose Up
-- Content safety (docs/DESIGN.md, "Safety"): the guests' acceptance of the
-- terms and the reports about content. Neither table has a foreign key to
-- rooms or accounts: a report must survive the deletion of the room it is
-- about and of the accounts it names (reporter_user and target_user are plain
-- ids, never joined to users).

-- One row per (guest, version of the terms). The guest id is the id of the
-- signed tf_guest cookie. No birth date: only the confirmation of being 13+.
CREATE TABLE terms_acceptances (
  guest_id        text        NOT NULL,
  version         text        NOT NULL,
  accepted_at     timestamptz NOT NULL,
  locale          text        NOT NULL,
  age13_confirmed boolean     NOT NULL,
  PRIMARY KEY (guest_id, version),
  CONSTRAINT terms_acceptances_age13 CHECK (age13_confirmed)
);

-- Content reports. evidence_json is what the room copied from the reporter's
-- authorized view (evidence_verified) or, when the room was gone, what the
-- client says it saw. purge_at is created_at + 90 days, or resolved_at + 30
-- days once a moderator resolved it, whichever is earlier; a daily job deletes
-- the rows past it.
CREATE TABLE reports (
  id                uuid        PRIMARY KEY,
  reporter_guest    text        NOT NULL,
  reporter_user     uuid,
  room_instance     text        NOT NULL,
  match_instance    text,
  content_key       text        NOT NULL,
  kind              text        NOT NULL,
  target_guest      text,
  target_user       uuid,
  evidence_json     jsonb       NOT NULL,
  evidence_verified boolean     NOT NULL,
  reason            text        NOT NULL,
  comment           text        NOT NULL DEFAULT '',
  status            text        NOT NULL DEFAULT 'open',
  idempotency_key   text        NOT NULL,
  created_at        timestamptz NOT NULL,
  resolved_at       timestamptz,
  purge_at          timestamptz NOT NULL,
  CONSTRAINT reports_idempotency UNIQUE (reporter_guest, idempotency_key)
);
CREATE INDEX reports_status_created_idx ON reports (status, created_at);
CREATE INDEX reports_purge_at_idx ON reports (purge_at);

-- +goose Down
DROP TABLE reports;
DROP TABLE terms_acceptances;
