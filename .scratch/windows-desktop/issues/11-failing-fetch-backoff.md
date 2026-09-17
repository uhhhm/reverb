# 11: A file that cannot be fetched stops crowding out the ones that can

**What to build:** File replication keeps making progress even when some files can never be fetched. Each pull round takes a bounded number of missing files from a peer and attempts them; a failure is logged and the round moves on, which is right for a transient error but wrong for a permanent one. A file that fails for a reason that will not change — the peer no longer holds the content, the hash never matches, or the path cannot be represented on this device's filesystem — is retried on every round forever, and because the round is capped it occupies a slot that a fetchable file could have used. Enough permanently-failing entries and replication stalls completely while the log fills with the same errors.

The unrepresentable-path case is the one that makes this urgent for a mixed-platform household: a Windows device pulling from a Linux or macOS peer will meet names it cannot write, and ticket 10 does not help for libraries downloaded before it. That case is also recognisable before any network work happens, so it should be filtered out at selection time rather than discovered as a write failure.

The owner needs to learn that a file is unfetchable and why. A file silently absent forever is worse than one reported as failed, because the owner cannot tell it apart from one still in flight.

**Blocked by:** 09 (Windows CI exercises the replication stack)

**Status:** done

- [x] A file that repeatedly fails to fetch backs off instead of being retried at full rate on every round, and stops consuming the per-round budget that fetchable files need.
- [x] A remote path this device's filesystem cannot represent is recognised when the round's candidates are chosen, not by attempting the write and failing.
- [x] Backing off is not giving up: a file that failed for a transient reason is attempted again once conditions may have changed, and one that starts succeeding replicates normally.
- [x] The owner can see which files are not replicating and why, rather than inferring it from a log.
- [x] With a peer advertising files that can never be fetched, the files that can be fetched still replicate to completion.
