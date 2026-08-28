-- name: TrustPeer :exec
INSERT INTO p2p_peer (peer_id, device_id, name, added_at, last_seen) VALUES (?, ?, ?, unixepoch(), unixepoch())
ON CONFLICT(peer_id) DO UPDATE SET device_id = excluded.device_id, name = excluded.name, last_seen = unixepoch();

-- name: GetTrustedPeer :one
SELECT * FROM p2p_peer WHERE peer_id = ?;

-- name: ListTrustedPeers :many
SELECT * FROM p2p_peer ORDER BY added_at;

-- name: DeleteTrustedPeer :exec
DELETE FROM p2p_peer WHERE peer_id = ?;

-- name: DeleteTrustedPeersByDevice :exec
DELETE FROM p2p_peer WHERE device_id = ?;

-- name: TouchTrustedPeer :exec
UPDATE p2p_peer SET last_seen = unixepoch() WHERE peer_id = ?;
