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

-- name: SetDevicePublicKey :exec
UPDATE device SET public_key = ? WHERE id = ?;

-- name: GetDevicePublicKey :one
SELECT public_key FROM device WHERE id = ?;

-- name: UpsertPeerDevice :exec
INSERT INTO device (id, name, token_hash, is_server, public_key, created_at, last_seen)
VALUES (?, ?, ?, 0, ?, unixepoch(), unixepoch())
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  last_seen = unixepoch(),
  public_key = CASE WHEN device.public_key = '' THEN excluded.public_key ELSE device.public_key END;

-- name: ListDevicesWithKeys :many
SELECT id, name, public_key FROM device WHERE public_key != '' ORDER BY created_at;
