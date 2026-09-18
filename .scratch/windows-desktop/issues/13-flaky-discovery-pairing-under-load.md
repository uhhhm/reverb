# 13: Pairing by discovery stops failing when the machine is busy

**What to build:** `TestTwoDevicesConvergeOverP2P` passes reliably whatever else is running. Today it fails roughly one run in three under `make test-race`, where Go runs every package's test binary at once and the machine is saturated — `gamma POST /p2p/pair/redeem` answers 500 instead of 200 (`internal/app/sync_e2e_test.go:387`). Run the package alone and it passes every time.

The failing call is the discovery form of pairing: `pairByDiscovery` connects the two hosts and then redeems with an empty `peerId`, which makes `RedeemViaDiscoveredPeers` try every connected peer that advertises the pairing protocol. A fresh libp2p connection does not know what the other end speaks until the identify exchange completes, so under CPU starvation the redeem runs against a peer set that is still empty and there is nothing to try.

This is not new and not caused by the Windows work: `git stash`ing the branch and running the same race suite against `a2ec869` reproduces it at the same line. It matters now because that suite is about to run on the Windows CI job too, where a shared runner is slower and more contended than a developer's machine — a flake here will read as a replication regression and train everyone to rerun the job.

The fix belongs in the product, not the test, if `RedeemViaDiscoveredPeers` giving up on an empty peer set is the real behaviour an owner would hit on a slow device; a peer that has connected but not yet identified is a peer worth waiting briefly for. If it is genuinely test-only setup, the test should wait for the protocol to be advertised before redeeming rather than sleeping.

**Blocked by:** None

**Status:** done

- [x] The two-device end-to-end test passes repeatedly under `make test-race` on a loaded machine.
- [x] Whichever layer is at fault is the one changed: a real race in `RedeemViaDiscoveredPeers` is fixed there, and only a genuinely test-only setup gap is fixed in the test.
- [x] Pairing by code alone on a LAN still works, and pairing with an explicit address is unaffected.
- [x] The fix does not mask a failure by retrying until the deadline: a peer set that stays empty still reports an error the owner can act on.

## Comments

Fixed in the product, not the test. `RedeemViaDiscoveredPeers` took an empty
candidate set as proof that no Reverb device was on the network, but libp2p
reports a connection before identify has said what the other end speaks, so a
redeem entered moments after discovery — or on a device slow enough that
identify has not finished — saw a connected household peer as no peer at all.
`awaitPairablePeers` now waits for that, and `internal/app/sync_e2e_test.go` is
untouched: `pairByDiscovery` still redeems immediately after `Connect`, which is
exactly the pre-identify state, so the test exercises the race rather than
stepping around it.

The wait ends on whichever event makes the answer final, not on a timer:
identify completing, identify failing, or a peer's connectedness changing. A
failure needs its own record, because libp2p writes nothing to the peerstore
when identify fails, so by peerstore state alone a peer that will never be
identified is indistinguishable from one still being identified. Nothing is
masked: with no peer connected the redeem still fails at once with the same
actionable error, and a caller that gives up now gets `ctx.Err()` rather than a
network diagnosis it did not earn.

One limit is documented rather than papered over: an identify failure that
landed before the subscription is not replayed and cannot be recovered through
the public `host.Host` API, so that peer is waited on until the grace expires —
a bounded delay before the same error, which is what the grace is for.

Verified by mutation: removing the failure tracking, or ignoring the events
entirely, each fail the new tests. Before the fix the tests failed; `make check`,
`make vet-windows` and three consecutive `make test-race` runs are green.
