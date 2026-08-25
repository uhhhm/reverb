-- name: CreateDevice :exec
INSERT INTO device (id, name, token_hash, is_server, created_at, last_seen) VALUES (?, ?, ?, ?, unixepoch(), unixepoch());

-- name: GetDeviceByID :one
SELECT * FROM device WHERE id = ?;

-- name: GetDeviceByTokenHash :one
SELECT * FROM device WHERE token_hash = ?;

-- name: ListDevices :many
SELECT * FROM device ORDER BY created_at;

-- name: DeleteDevice :exec
DELETE FROM device WHERE id = ?;

-- name: TouchDeviceLastSeen :exec
UPDATE device SET last_seen = unixepoch() WHERE id = ?;
