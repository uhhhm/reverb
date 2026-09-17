# 13: Pairing by discovery stops failing when the machine is busy

**What to build:** `TestTwoDevicesConvergeOverP2P` passes reliably whatever else is running. Today it fails roughly one run in three under `make test-race`, where Go runs every package's test binary at once and the machine is saturated — `gamma POST /p2p/pair/redeem` answers 500 instead of 200 (`internal/app/sync_e2e_test.go:387`). Run the package alone and it passes every time.

The failing call is the discovery form of pairing: `pairByDiscovery` connects the two hosts and then redeems with an empty `peerId`, which makes `RedeemViaDiscoveredPeers` try every connected peer that advertises the pairing protocol. A fresh libp2p connection does not know what the other end speaks until the identify exchange completes, so under CPU starvation the redeem runs against a peer set that is still empty and there is nothing to try.

This is not new and not caused by the Windows work: `git stash`ing the branch and running the same race suite against `a2ec869` reproduces it at the same line. It matters now because that suite is about to run on the Windows CI job too, where a shared runner is slower and more contended than a developer's machine — a flake here will read as a replication regression and train everyone to rerun the job.

The fix belongs in the product, not the test, if `RedeemViaDiscoveredPeers` giving up on an empty peer set is the real behaviour an owner would hit on a slow device; a peer that has connected but not yet identified is a peer worth waiting briefly for. If it is genuinely test-only setup, the test should wait for the protocol to be advertised before redeeming rather than sleeping.

**Blocked by:** None

**Status:** ready-for-agent

- [ ] The two-device end-to-end test passes repeatedly under `make test-race` on a loaded machine.
- [ ] Whichever layer is at fault is the one changed: a real race in `RedeemViaDiscoveredPeers` is fixed there, and only a genuinely test-only setup gap is fixed in the test.
- [ ] Pairing by code alone on a LAN still works, and pairing with an explicit address is unaffected.
- [ ] The fix does not mask a failure by retrying until the deadline: a peer set that stays empty still reports an error the owner can act on.
