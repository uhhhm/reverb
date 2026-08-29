-- +goose Up
-- Measured playback gain per track, in dB, relative to Reverb's reference level.
-- The audio file itself is never touched: the gain is applied by the player, so
-- normalization can be switched off instantly and losslessly.
-- track_id is the library-backend track id.
CREATE TABLE track_loudness (
  track_id   TEXT PRIMARY KEY,
  gain_db    REAL NOT NULL,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE track_loudness;
