-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_single_server ON device(is_server) WHERE is_server = 1;

-- +goose Down
DROP INDEX IF EXISTS idx_device_single_server;
