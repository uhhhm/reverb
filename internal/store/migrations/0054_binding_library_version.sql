-- +goose Up
-- The library_version a binding was resolved against. A track known absent is
-- absent only from the library as it was then; existing rows record 0, so each
-- is matched once more.
ALTER TABLE backend_binding ADD COLUMN library_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE backend_binding DROP COLUMN library_version;
