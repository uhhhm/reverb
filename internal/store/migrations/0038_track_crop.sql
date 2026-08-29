-- +goose Up
-- Non-destructive trim points for a track. The audio file is never rewritten:
-- playback starts at start_ms and stops at end_ms, so a crop can be changed or
-- removed at any time without having lost anything.
-- end_ms IS NULL means "play to the end of the file".
-- track_id is the library-backend track id.
-- catalog_id is the stable identity a crop syncs under: the backend track_id
-- changes when the library backend is swapped, the catalog id does not.
CREATE TABLE track_crop (
  track_id   TEXT PRIMARY KEY,
  start_ms   INTEGER NOT NULL DEFAULT 0,
  end_ms     INTEGER,
  updated_at INTEGER NOT NULL,
  catalog_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_track_crop_catalog ON track_crop(catalog_id);

-- +goose Down
DROP TABLE track_crop;
