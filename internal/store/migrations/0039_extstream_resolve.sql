-- +goose Up
-- What an external (not-in-library) track resolved to, so playing it a second
-- time does not repeat work that takes seconds.
--
-- Two facts with very different lifetimes share a row. video_id is the search
-- result — which track on the upstream this is — and never changes, so it is
-- kept forever and spares every later resolve the search stage. url is a signed,
-- expiring media URL; it is only usable until url_expires_at, after which the
-- row still saves the search.
CREATE TABLE extstream_resolve (
  source          TEXT NOT NULL,
  external_id     TEXT NOT NULL,
  video_id        TEXT NOT NULL,
  url             TEXT NOT NULL DEFAULT '',
  url_expires_at  INTEGER NOT NULL DEFAULT 0,
  updated_at      INTEGER NOT NULL,
  PRIMARY KEY (source, external_id)
);

-- +goose Down
DROP TABLE extstream_resolve;
