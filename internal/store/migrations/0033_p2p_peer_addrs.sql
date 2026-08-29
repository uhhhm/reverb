-- +goose Up
-- Remember how to reach a paired peer. libp2p resolves a peer ID to addresses
-- through the peerstore, which is populated by mDNS or the DHT -- neither of
-- which works over a VPN: multicast does not cross the tunnel, and the DHT runs
-- in client mode advertising addresses that are not globally routable. Without
-- a stored address a paired peer is undialable after a restart even though the
-- two machines can reach each other perfectly well.
--
-- addrs is a newline-separated list of full multiaddrs including /p2p/<peerID>.

ALTER TABLE p2p_peer ADD COLUMN addrs TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE p2p_peer DROP COLUMN addrs;
