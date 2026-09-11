-- name: UpsertNotInterested :exec
INSERT INTO not_interested (key, kind, title, artist, source, external_id, marked_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  kind = excluded.kind,
  title = excluded.title,
  artist = excluded.artist,
  source = excluded.source,
  external_id = excluded.external_id,
  marked_at = excluded.marked_at;

-- name: DeleteNotInterested :exec
DELETE FROM not_interested WHERE key = ?;

-- name: ListNotInterested :many
SELECT * FROM not_interested ORDER BY marked_at DESC, key;
