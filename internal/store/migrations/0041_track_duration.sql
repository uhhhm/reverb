-- +goose Up
-- The measured playable length of a track, in milliseconds.
--
-- This is not the tag's duration: it is what came out of a decode, which is the
-- only source that survives a VBR header extrapolated from the first frames or
-- a file re-muxed without its metadata being fixed. It describes one file, so
-- it is keyed on the library-backend track id and never replicated — a paired
-- device measures its own copy.
CREATE TABLE track_duration (
  track_id    TEXT PRIMARY KEY,
  duration_ms INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE track_duration;
