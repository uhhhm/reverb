# 09: Windows CI exercises the replication stack

**What to build:** A Windows CI run that proves sync actually works there, not just that the desktop shell builds. Today the Windows job runs only the desktop, paths, embedded-library and child-process suites, so nothing has ever compiled or exercised the sync, P2P, or two-device end-to-end tests on Windows — the platform where a cross-platform household would first hit trouble. Extending the job to cover them turns "the code looks portable" into evidence, and makes every later cross-platform fix verifiable instead of assumed.

Expect the first run to surface real failures; fixing them is part of this ticket, not a follow-up. One is already known: a security test creates a symlink to prove the file handler refuses an escape out of the music directory, and symlink creation on a Windows runner needs a privilege the default token lacks. That test must still run its assertion on Windows or skip for a stated reason — dropping the whole package to get green would delete the coverage this ticket exists to add.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] The Windows CI job runs the sync, P2P, and two-device end-to-end suites alongside the suites it already covers, and passes.
- [x] Tests that cannot run on Windows for a platform reason skip explicitly with that reason stated, rather than being excluded at the package level; no assertion is silently lost.
- [x] Any genuine cross-platform defect the new coverage surfaces is fixed here, or filed as its own ticket if it is out of scope.
- [x] A regression in replication behaviour on Windows fails CI rather than reaching a release.
