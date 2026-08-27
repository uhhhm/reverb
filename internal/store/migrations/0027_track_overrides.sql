-- +goose Up
-- User-supplied display names for library tracks. Reverb never rewrites file
-- tags, so a rename lives here and is applied when tracks are read out.
-- track_id is the library-backend track id.
CREATE TABLE track_override (
  track_id   TEXT PRIMARY KEY,
  title      TEXT NOT NULL DEFAULT '',
  artist     TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE track_override;
