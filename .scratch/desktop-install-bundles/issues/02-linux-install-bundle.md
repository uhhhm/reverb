# 02: Linux install bundle, built and installed locally

**What to build:** A Linux user can do a real first install. One make target produces `Reverb-linux-<arch>.tar.gz` holding the desktop binary, the bundled tools (ffmpeg, Navidrome, deno, and the relocatable Python runtime with spotDL and yt-dlp), the desktop entry, the app icon, and an install/uninstall script. Extracting it and running the script installs Reverb per-user (no root) into a user-writable location, registers the desktop entry and icon, and makes Reverb show up in the desktop's app launcher search (KDE Plasma, GNOME). The install location is writable so self-update keeps working: the updater replaces only the binary and the tools beside it stay in place.

This is a tarball plus a script rather than an AppImage on purpose: an AppImage is a read-only image the updater cannot swap a binary inside.

**Blocked by:** 01 (Shared relocatable Python runtime build)

**Status:** ready-for-agent

- [ ] A make target builds the tarball for the host arch from already-fetched tools, failing clearly if any tool is missing.
- [ ] The install script installs per-user without root, writes a desktop entry whose `Exec` and `Icon` point at the installed files (no reliance on `PATH` or an icon-theme name that doesn't exist), and refreshes the desktop database when the tool is available.
- [ ] After install on Nobara/KDE, Reverb appears in launcher search with its icon, launches, and the window groups under its own taskbar entry.
- [ ] A download completes using only bundled tools, with none of them on the system `PATH`.
- [ ] Re-running the install script over an existing install upgrades it in place without losing app data; the uninstall script removes the installed files, desktop entry and icon, and leaves user data alone.
- [ ] Self-update from an installed bundle replaces only the binary, relaunches, and the bundled tools still resolve afterwards.
