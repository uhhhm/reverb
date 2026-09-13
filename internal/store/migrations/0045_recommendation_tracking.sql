-- +goose Up
ALTER TABLE plays ADD COLUMN origin TEXT NOT NULL DEFAULT '';
ALTER TABLE plays ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plays ADD COLUMN qualified INTEGER NOT NULL DEFAULT 1;
CREATE INDEX idx_plays_session ON plays(session_id) WHERE session_id != '';
CREATE INDEX idx_plays_origin_time ON plays(origin, played_at) WHERE origin != '';

CREATE TABLE recommendation_add (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL,
  origin     TEXT NOT NULL,
  action     TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_recommendation_add_origin_time ON recommendation_add(origin, created_at);

-- +goose Down
DROP TABLE recommendation_add;
DROP INDEX idx_plays_origin_time;
DROP INDEX idx_plays_session;
ALTER TABLE plays DROP COLUMN qualified;
ALTER TABLE plays DROP COLUMN session_id;
ALTER TABLE plays DROP COLUMN origin;
