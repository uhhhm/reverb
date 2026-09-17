# 06: Windows self-update

**What to build:** The desktop updater works on Windows end to end: it recognises the Windows release asset, downloads and verifies it, and on **Restart now** replaces the running executable, relaunches, and cleans up after the successor is up. The successor waits for the predecessor to exit before touching the database or the bundled library, exactly as on the other platforms, and a failed swap rolls back to the working binary. A truncated or non-executable payload is refused.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-human

- [x] Asset selection recognises the Windows zip for the current architecture, and the updater unit test that currently asserts Windows is unsupported asserts it is chosen instead.
- [x] Executable verification accepts a valid Windows binary and refuses a truncated download or an HTML error page.
- [ ] On Windows, installing a staged update replaces the running binary, relaunches the updated build, and the successor waits for the predecessor to exit before opening the database or starting the bundled library.
- [x] After a successful update the previous binary and staged payload are removed; an update staged for a newer release than the running build survives.
- [x] If the swap fails partway, the original binary is restored and the app still starts.
- [x] If Windows refuses to rename the running executable, a swap helper or equivalent documented mechanism performs the replacement; the chosen mechanism and its failure modes are documented.
- [x] Update state, prompts, and the Restart now / Later flow are unchanged for macOS/Linux.

## Comments

`PickAsset` already selected by `GOOS/GOARCH`; the unit test now asserts the
Windows zip is chosen rather than that nothing is. Executable verification
gained a PE branch, and is parameterised by platform so every host's magic
number is checked from any runner. The release zip carries
`reverb-desktop.exe`, so the payload entry name is now platform-derived.

The swap needs no helper: Windows locks a mapped image against deletion and
writes but not against a rename within its volume, so the existing
rename-aside/rename-in ordering is the mechanism. The one case it refuses —
reusing a `.old` name still mapped by a live process — now falls back to a
uniquely named sibling, which `CleanupAfterUpdate` collects alongside the
preferred name via `existingBackups`. That fallback is not Windows-gated: on
macOS and Linux a backup name that cannot be unlinked used to fail the whole
swap, and now diverts the same way. Update state, prompts and the Restart
now / Later flow are untouched on those platforms. `ApplyStaged` returns the backup path so
rollback restores the exact file it moved. The remaining case — a rename the
filesystem refuses outright, from antivirus or a shell holding the folder —
rolls back to the working binary rather than half-installing; that and the rest
of the mechanism are documented in `platform_windows.go` and
`desktop/README.md`. No Windows runner exercises the swap against a live image,
so that rests on the documented behaviour until the hardware pass.

Windows CI compiles and runs the updater tests. Installing a staged update over
a running packaged `reverb-desktop.exe`, relaunching it, and confirming the
successor waits for the predecessor still needs a real Windows machine.
