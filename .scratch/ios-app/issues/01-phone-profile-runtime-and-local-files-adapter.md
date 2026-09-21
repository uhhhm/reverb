# 01: Phone-profile runtime and local-files library adapter

**What to build:** The shared composition root can build a phone-profile Device: a pure-Go local-files library adapter instead of Navidrome/Subsonic, and none of the desktop-only services (background agent, desktop updater, bundled executables). A phone-profile runtime pairs with a desktop runtime and receives its playlists and library metadata through ordinary sync. This is the prefactor every other phone ticket builds on. See ADR 0003 and the spec.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] The local-files adapter indexes a directory (tags, art, streams), is registered explicitly at the composition root, and passes the existing `library` conformance suite
- [x] The composition root builds a phone profile without starting Navidrome, bundled tools, or desktop-only services; the desktop and server profiles are unchanged
- [x] The multi-runtime e2e harness can boot a phone-profile runtime next to a desktop runtime
- [x] E2E: a phone-profile runtime pairs with a desktop runtime by typed code and converges on the desktop's playlists, plays, Not interested marks, and settings, in both directions
- [x] `make check` passes
