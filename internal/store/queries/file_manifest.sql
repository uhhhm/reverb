-- name: UpsertFileManifest :exec
INSERT INTO file_manifest (canonical_id, content_hash, size, rel_path, mtime, device_id) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(canonical_id) DO UPDATE SET content_hash = excluded.content_hash, size = excluded.size, rel_path = excluded.rel_path, mtime = excluded.mtime, device_id = excluded.device_id;

-- name: GetFileManifest :one
SELECT * FROM file_manifest WHERE canonical_id = ?;

-- name: ListFileManifests :many
SELECT * FROM file_manifest ORDER BY canonical_id;

-- name: ListFileManifestsByHash :many
SELECT * FROM file_manifest WHERE content_hash = ?;

-- name: DeleteFileManifest :exec
DELETE FROM file_manifest WHERE canonical_id = ?;

-- name: CountFileManifests :one
SELECT COUNT(*) FROM file_manifest;

-- name: UpsertFileTag :exec
INSERT INTO file_tag (content_hash, title, artist, album, isrc) VALUES (?, ?, ?, ?, ?) ON CONFLICT(content_hash) DO UPDATE SET title = excluded.title, artist = excluded.artist, album = excluded.album, isrc = excluded.isrc;

-- name: ListFileTags :many
SELECT * FROM file_tag;

-- name: DeleteOrphanFileTags :exec
DELETE FROM file_tag WHERE content_hash NOT IN (SELECT content_hash FROM file_manifest);
