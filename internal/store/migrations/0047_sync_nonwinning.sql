-- +goose Up
-- Losing but authentic changes still travel so downstream vectors can advance.
-- They must not replace the last accepted value in the materialized view.
CREATE TABLE sync_nonwinning (
    revision INTEGER PRIMARY KEY REFERENCES sync_change(revision) ON DELETE CASCADE
);

-- Ask authors for history older builds discarded on conflict. Never reset the
-- local sequence: that would reuse sequence numbers already sent to peers.
UPDATE sync_vector SET seq = 0
WHERE EXISTS (SELECT 1 FROM settings WHERE key = 'local_device_id')
  AND device_id != (SELECT value FROM settings WHERE key = 'local_device_id');

-- +goose Down
DELETE FROM sync_change WHERE revision IN (SELECT revision FROM sync_nonwinning);
DROP TABLE sync_nonwinning;
