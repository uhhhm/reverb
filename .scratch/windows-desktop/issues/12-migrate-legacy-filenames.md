# 12: Existing libraries get portable filenames

**What to build:** A library downloaded before portable naming landed becomes replicable to a Windows device. Ticket 10 only governs names minted from that point on; an owner who has been running Reverb on Linux or macOS already has tracks on disk whose names Windows cannot store, and those stay permanently stuck — ticket 11 makes the failure visible but cannot resolve it. Pairing a Windows device to an established library is exactly the case this product is for, so leaving it broken undermines the Windows work.

The migration renames the offending files in place and carries the library with them: the file manifest tracks a file by a stable identity with the path as an attribute, so a rename updates the existing entry rather than creating a second one, and the content hash is unchanged so peers that already hold the bytes must not re-fetch them. The embedded library backend also indexes these files and has to end up consistent with what is on disk rather than serving paths that no longer exist.

This touches files in the owner's own music folder, which is the most destructive thing Reverb does to data it did not create. It must be safe to interrupt: a migration killed halfway leaves every file either at its old name or its new one, with the library agreeing, and never a half-renamed or orphaned file. A name collision with a file already present must not destroy either one.

**Blocked by:** 10 (Downloads mint filenames every platform can store)

**Status:** done

- [x] Existing files whose names cannot be stored on another platform are renamed to the same portable form that new downloads now receive.
- [x] The file manifest follows the rename in place: identity and content hash are unchanged, and peers already holding the content do not re-fetch it.
- [x] The embedded library and the catalog stay consistent with the renamed files; playback, artwork and listening history for a migrated track are unaffected.
- [x] Interrupting the migration — a crash, a force quit, a closed lid — leaves every file at either its old or its new name with the library agreeing, and rerunning it completes the job.
- [x] A rename that would collide with an existing file resolves without either file being lost or overwritten.
- [x] After migration, a previously stuck track replicates to a Windows peer.
