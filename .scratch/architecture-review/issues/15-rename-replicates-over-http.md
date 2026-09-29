# 15 — A rename made over HTTP on one device shows on its peer

**What to build:** An e2e test in `internal/app` with two paired devices. The owner renames a library track through the HTTP API on device A, and the new name is visible on device B. No test covers that path end to end today, although a rename crosses api → metadata → override → syncemit → catalog → sync → p2p → materialize.

This is the safety net for ticket 08's refactor. It is split out so it lands before that refactor starts, and it must pass unchanged afterwards.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Write the test first against the current code. If it fails, that is a bug: record it here and fix it before closing.
- [ ] Build on the two-device setup in `internal/app/sync_e2e_test.go` (`TestTwoDevicesConvergeOverP2P`). Sync over the real P2P transport, not by copying logs.
- [ ] Use a medium-hard scenario, not a single rename:
  - Rename title, artist and album of one track on A.
  - Set a crop on a second track.
  - Rename the first track again on B before syncing back.
  - Both devices converge on the later edit for each field.
  - Neither device's log gains an echo of a change it received.
  - Both devices' HTTP track and library responses show the converged names.
- [ ] B learns the track only through the catalog id, never a backend id from A. The test asserts that the renamed track on B resolves through B's own binding.
- [ ] The test writes a small artifact under the test's output directory: the two devices' final track views and the change ids exchanged. It records the exact rerun command in a comment at the top of the test.
- [ ] `go test ./internal/app -run <TestName>` passes, and so does `go test ./internal/app`.
