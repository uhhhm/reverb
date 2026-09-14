-- +goose Up
-- Listening history imported from a linked scrobbling account. It is a taste
-- input only: it never becomes plays and never replicates. A row is either an
-- aggregate (a top track or top artist, dated when the account was linked) or
-- one day's scrobbles of a track from before Reverb. Unlinking the account
-- deletes its rows.
CREATE TABLE taste_history (
  provider TEXT NOT NULL,
  artist   TEXT NOT NULL,
  title    TEXT NOT NULL DEFAULT '',
  plays    INTEGER NOT NULL,
  at       INTEGER NOT NULL
);
CREATE INDEX idx_taste_history_provider ON taste_history (provider);

-- +goose Down
DROP TABLE taste_history;
