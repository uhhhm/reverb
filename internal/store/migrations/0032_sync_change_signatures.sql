-- +goose Up
-- Per-device change signatures. Without them a relayed change is
-- indistinguishable from one the relaying peer invented, so peers could only
-- safely accept changes authored by whoever handed them over — which confines
-- convergence to a fully paired mesh. A signature lets any peer verify
-- authorship independently of who delivered it.
--
-- device.public_key is the Ed25519 verification key, matching the device's
-- libp2p peer ID. Empty means "not yet known": pre-existing local rows and
-- devices paired before this migration have no key and their changes are
-- treated as locally-authored only.

ALTER TABLE sync_change ADD COLUMN sig TEXT NOT NULL DEFAULT '';
ALTER TABLE device ADD COLUMN public_key TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns before 3.35; these are additive and harmless.
