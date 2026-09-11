-- +goose Up
-- The owner's Not interested marks: tracks and artists never to recommend.
-- A mark is a replicated fact (entity "notInterested", field "mark"), so the
-- key is an identity every device agrees on — a name-derived key for artists,
-- a metadata fingerprint for library tracks, source + external id otherwise —
-- never a backend id. This table is the projection the app reads.
CREATE TABLE not_interested (
  key         TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  artist      TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '',
  marked_at   INTEGER NOT NULL
);

-- +goose Down
DROP TABLE not_interested;
