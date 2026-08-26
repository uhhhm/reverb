-- name: CreatePairingCode :exec
INSERT INTO pairing_code (code, expires_at, created_at) VALUES (?, ?, unixepoch());

-- name: GetPairingCode :one
SELECT * FROM pairing_code WHERE code = ?;

-- name: MarkPairingCodeUsed :exec
UPDATE pairing_code SET used_at = unixepoch(), used_by_device_id = ? WHERE code = ?;

-- name: TryMarkPairingCodeUsed :execrows
UPDATE pairing_code SET used_at = unixepoch(), used_by_device_id = ? WHERE code = ? AND used_at IS NULL AND expires_at > unixepoch();

-- name: DeleteExpiredPairingCodes :exec
DELETE FROM pairing_code WHERE expires_at < unixepoch() AND used_at IS NULL;

-- name: ListPairingCodes :many
SELECT * FROM pairing_code ORDER BY created_at DESC;
