-- +goose Up
-- Non-destructive trim points for a track. The audio file is never rewritten:
-- playback starts at start_ms and stops at end_ms, so a crop can be changed or
-- removed at any time without having lost anything.
-- end_ms IS NULL means "play to the end of the file".
-- track_id is the library-backend track id.
CREATE TABLE track_crop (
  track_id   TEXT PRIMARY KEY,
  start_ms   INTEGER NOT NULL DEFAULT 0,
  end_ms     INTEGER,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE track_crop;
