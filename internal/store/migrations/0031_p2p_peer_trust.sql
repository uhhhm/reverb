-- +goose Up
-- Bind libp2p peer identities to paired devices. Before this, the p2p stream
-- handlers had no notion of who was connected: /reverb/file/1.0.0 served any
-- file under musicDir to any dialer, and /reverb/sync/1.0.0 accepted a bare
-- self-asserted device_id as its only credential. p2p_peer is the trust set;
-- a peer is added only by completing a pairing-code exchange.

CREATE TABLE IF NOT EXISTS p2p_peer (
  peer_id   TEXT PRIMARY KEY,
  device_id TEXT REFERENCES device(id) ON DELETE CASCADE,
  name      TEXT NOT NULL DEFAULT '',
  added_at  INTEGER NOT NULL DEFAULT (unixepoch()),
  last_seen INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_p2p_peer_device ON p2p_peer(device_id);

-- +goose Down
DROP INDEX IF EXISTS idx_p2p_peer_device;
DROP TABLE IF EXISTS p2p_peer;
