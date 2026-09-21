# 03: Pairing QR payload (desktop side)

**What to build:** The desktop pairing screen shows a QR code alongside the pairing code. It encodes a versioned payload: the code plus this device's reachable multiaddrs (LAN and VPN). A new redeem path accepts that payload and pairs through the existing mutual challenge-response, so the code still never crosses the network. Redeeming a typed code is unchanged.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] The pairing API returns a versioned QR payload with the code and the responder's reachable multiaddrs, and the desktop pairing screen renders it as a QR code
- [ ] A redeem endpoint accepts the payload, dials the multiaddrs it carries, and pairs with the existing proof of possession
- [ ] E2E: two runtimes pair from a QR payload without mDNS discovery
- [ ] A malformed payload, an unknown payload version, or an expired code is rejected with a clear error
- [ ] OpenAPI updated and `make contracts` run
