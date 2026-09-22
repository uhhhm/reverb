-- +goose Up
-- What a file's tags say, read when the file is hashed for the manifest. A
-- phone keeps only its offline set, so it has to find the files a playlist
-- names among everything a peer advertises; the playlist names tracks by title,
-- artist and album, and the manifest by path and hash, and these rows are the
-- bridge. Keyed on the content, so a rename or a portable-name migration does
-- not have to carry them across.
CREATE TABLE IF NOT EXISTS file_tag (
  content_hash TEXT PRIMARY KEY,
  title        TEXT NOT NULL DEFAULT '',
  artist       TEXT NOT NULL DEFAULT '',
  album        TEXT NOT NULL DEFAULT '',
  isrc         TEXT NOT NULL DEFAULT ''
);

-- The files this device fetched because its offline set wanted them. Pruning
-- removes only these: a file that reached the folder any other way is not the
-- offline set's to delete. Local-only, like offline_set.
CREATE TABLE IF NOT EXISTS offline_file (
  rel_path     TEXT PRIMARY KEY,
  content_hash TEXT NOT NULL,
  fetched_at   INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS offline_file;
DROP TABLE IF EXISTS file_tag;
