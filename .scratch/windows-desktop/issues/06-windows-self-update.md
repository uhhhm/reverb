# 06: Windows self-update

**What to build:** The desktop updater works on Windows end to end: it recognises the Windows release asset, downloads and verifies it, and on **Restart now** replaces the running executable, relaunches, and cleans up after the successor is up. The successor waits for the predecessor to exit before touching the database or the bundled library, exactly as on the other platforms, and a failed swap rolls back to the working binary. A truncated or non-executable payload is refused.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-agent

- [ ] Asset selection recognises the Windows zip for the current architecture, and the updater unit test that currently asserts Windows is unsupported asserts it is chosen instead.
- [ ] Executable verification accepts a valid Windows binary and refuses a truncated download or an HTML error page.
- [ ] On Windows, installing a staged update replaces the running binary, relaunches the updated build, and the successor waits for the predecessor to exit before opening the database or starting the bundled library.
- [ ] After a successful update the previous binary and staged payload are removed; an update staged for a newer release than the running build survives.
- [ ] If the swap fails partway, the original binary is restored and the app still starts.
- [ ] If Windows refuses to rename the running executable, a swap helper or equivalent documented mechanism performs the replacement; the chosen mechanism and its failure modes are documented.
- [ ] Update state, prompts, and the Restart now / Later flow are unchanged for macOS/Linux.
