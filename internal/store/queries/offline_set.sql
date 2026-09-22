-- name: UpsertOfflineSet :exec
INSERT INTO offline_set (device_id, playlist_id, enabled, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(device_id, playlist_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at;

-- name: ListOfflineSetForDevice :many
SELECT * FROM offline_set WHERE device_id = ? ORDER BY playlist_id;

-- name: GetOfflineSetEntry :one
SELECT * FROM offline_set WHERE device_id = ? AND playlist_id = ?;

-- name: DeleteOfflineSetEntry :exec
DELETE FROM offline_set WHERE device_id = ? AND playlist_id = ?;

-- name: DeleteOfflineSetForPlaylist :exec
DELETE FROM offline_set WHERE playlist_id = ?;

-- name: UpsertOfflineFile :exec
INSERT INTO offline_file (rel_path, content_hash, fetched_at) VALUES (?, ?, ?) ON CONFLICT(rel_path) DO UPDATE SET content_hash = excluded.content_hash, fetched_at = excluded.fetched_at;

-- name: ListOfflineFiles :many
SELECT * FROM offline_file ORDER BY rel_path;

-- name: DeleteOfflineFile :exec
DELETE FROM offline_file WHERE rel_path = ?;
