# 07: Visible startup failures and second-launch handling

**What to build:** When the desktop app cannot start, the user finds out why instead of seeing nothing. Today every startup error ends the process through a fatal log write to stderr, and a Windows GUI-subsystem exe has no console, so the app closes without a trace. A friend's report that the Windows exe "does not open" could not be diagnosed for exactly this reason. After this ticket:

- The window process writes its log to a file in the data directory (as the background runtime already does), rotated or size-capped, on every platform.
- A fatal startup error shows a native error dialog naming the failure and the log file's location before the process exits. This matters most on Windows, but the dialog should appear on every platform where the window can't open.
- Launching the app while another instance already holds the data-directory lock brings the existing window to the front (or opens it, if the running instance is the headless background runtime) instead of exiting silently.
- A process that is still waiting on the webview runtime (for example while WebView2 is missing or being installed on Windows) does not leave the user stuck with an invisible instance: either the wait is surfaced to the user, or failing to get a webview is treated as a fatal startup error with the dialog above.

Found while investigating the Windows report: under Wine the backend boots fully, then sits windowless while Wails fetches the WebView2 bootstrapper. On real Windows that same state would make every later launch hit the single-instance lock and exit silently.

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] The window process logs to a file in the data directory on every platform; the file is size-capped or rotated, and the background runtime's existing log is unaffected.
- [x] Every startup error path that currently ends the process silently instead shows a native error dialog with the error and the log path, then exits non-zero. A test covers the dispatch from startup error to the dialog seam without needing a display.
- [x] A second launch while an instance holds the lock focuses the running window, or asks the background runtime to open one, and exits cleanly; it never exits silently with nothing on screen.
- [x] On Windows, a missing WebView2 runtime produces a visible outcome (an install prompt the user can see, or the error dialog), never an invisible process that holds the lock.
- [ ] Verified on a real Windows machine: an induced startup failure shows the dialog, and double-launching focuses the existing window.
- [ ] The desktop README tells users where the log file lives on each platform.
