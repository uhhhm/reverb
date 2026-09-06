-- +goose Up
-- Keep corrupt copies for diagnosis while anti-entropy fetches authentic rows.
CREATE TABLE sync_quarantine (
    revision INTEGER PRIMARY KEY,
    change_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    quarantined_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- +goose Down
DROP TABLE sync_quarantine;
