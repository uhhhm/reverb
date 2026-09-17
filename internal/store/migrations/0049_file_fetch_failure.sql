-- +goose Up
-- A file that can never be fetched must stop crowding out the ones that can.
--
-- Each pull round takes a bounded number of missing files from a peer. A
-- failure was logged and the round moved on, which is right for a transient
-- error and wrong for a permanent one: a file whose content the peer no longer
-- holds, whose hash never matches, or whose name this device's filesystem
-- cannot write was retried at full rate every round and occupied a slot a
-- fetchable file could have used.
--
-- Keyed on the peer as well as the content, because a round runs per peer and
-- most reasons are that peer's: one device that no longer holds the file must
-- not stop the same content being pulled from a healthy one. rel_path is the
-- path last attempted, which is both what the owner needs to see and how the
-- puller notices a peer has renamed the file — a new path is a new chance, not
-- a continuation of the old backoff.
CREATE TABLE IF NOT EXISTS file_fetch_failure (
  peer_id         TEXT NOT NULL,
  content_hash    TEXT NOT NULL,
  rel_path        TEXT NOT NULL,
  reason          TEXT NOT NULL,
  detail          TEXT NOT NULL DEFAULT '',
  attempts        INTEGER NOT NULL DEFAULT 0,
  first_failed_at INTEGER NOT NULL,
  last_failed_at  INTEGER NOT NULL,
  next_attempt_at INTEGER NOT NULL,
  PRIMARY KEY (peer_id, content_hash)
);
CREATE INDEX IF NOT EXISTS idx_file_fetch_failure_next ON file_fetch_failure(next_attempt_at);

-- +goose Down
DROP TABLE IF EXISTS file_fetch_failure;
