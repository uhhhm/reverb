# 01: Pairing must prove possession of the code before a peer is trusted

**What to build:** Pairing with the code alone on a local network keeps working against the device that generated the code, but a peer is only added to the trusted-device set and only receives a session token after it proves it holds that pairing code. Today the redeeming device offers the plaintext code to every connected peer that advertises the pairing protocol and trusts whichever one answers "success", so a rogue LAN peer can be trusted without ever knowing the code — and can also harvest the code and redeem it against the real device.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Pairing with a code and no explicit address still succeeds against the device that generated the code on the same LAN, and the new device appears in the paired-device list.
- [ ] A connected peer that advertises the pairing protocol but does not hold the code cannot get itself trusted, even by returning a well-formed success response with a plausible device ID.
- [ ] A peer that cannot prove possession of the code is refused rather than trusted (fail closed), so an older peer that cannot complete the proof is not silently trusted.
- [ ] The plaintext pairing code is never sent to a peer that has not first proven it holds that code.
- [ ] The explicit-address pairing path keeps the same proof requirement, so a user-supplied address cannot bypass it.
- [ ] A regression test runs a rogue peer that always reports success and asserts it is neither trusted nor able to obtain a token; a companion test asserts pairing with the real code holder still succeeds end to end.
- [ ] Pairing code single-use expiry and the existing per-peer/global attempt limiting are unchanged.
- [ ] `go test ./internal/p2p/... ./internal/sync/... ./internal/api/...` passes.
