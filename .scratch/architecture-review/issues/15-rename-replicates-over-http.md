# 15 — A rename made over HTTP on one device shows on its peer

**What to build:** An e2e test in `internal/app` with two paired devices. The owner renames a library track through the HTTP API on device A, and the new name is visible on device B. No test covers that path end to end today, although a rename crosses api → metadata → override → syncemit → catalog → sync → p2p → materialize.

This is the safety net for ticket 08's refactor. It is split out so it lands before that refactor starts, and it must pass unchanged afterwards.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Write the test first against the current code. If it fails, that is a bug: record it here and fix it before closing.
- [x] Build on the two-device setup in `internal/app/sync_e2e_test.go` (`TestTwoDevicesConvergeOverP2P`). Sync over the real P2P transport, not by copying logs.
- [x] Use a medium-hard scenario, not a single rename:
  - Rename title, artist and album of one track on A.
  - Set a crop on a second track.
  - Rename the first track again on B before syncing back.
  - Both devices converge on the later edit for each field.
  - Neither device's log gains an echo of a change it received.
  - Both devices' HTTP track and library responses show the converged names.
- [x] B learns the track only through the catalog id, never a backend id from A. The test asserts that the renamed track on B resolves through B's own binding.
- [x] The test writes a small artifact under the test's output directory: the two devices' final track views and the change ids exchanged. It records the exact rerun command in a comment at the top of the test.
- [x] `go test ./internal/app -run <TestName>` passes, and so does `go test ./internal/app`.

## Comments

Implemented as `TestLibraryRenameOverHTTPReplicatesToPeer` in `internal/app/rename_e2e_test.go`. Alpha is a downloading desktop; beta is a built-in desktop whose fake Navidrome (`folderSubsonicIDs`) addresses files under its own `b-` ids. Beta holds the files only through P2P file sync, so every edit it makes goes through its own binding. The artifact is `library-rename-replication.json`: both devices' final catalog and library views, and every rename and crop change in the log, with random ids replaced by labels.

The scenario adds one step to the list above. After beta renames the title and album, alpha changes only the artist before either side syncs, and each field must keep its own later edit.

**Bug found:** that step failed. `metadata.Edits.Rename` published title, artist and album on every rename, including fields the patch left out. So alpha's artist-only rename re-sent its older title and album with a newer clock and overwrote beta's edit. Fixed: `Rename` now publishes only the fields the patch names. `TestRenamePreservesOmissionsAndLocalEditOnPublicationFailure` now asserts that.
