-- +goose Up
-- Per-track download-quality override. Takes precedence over the global
-- download_quality setting when Reverb (re-)fetches this track.
-- track_id is the library-backend track id.
CREATE TABLE track_quality_override (
  track_id   TEXT PRIMARY KEY,
  quality    TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE track_quality_override;
