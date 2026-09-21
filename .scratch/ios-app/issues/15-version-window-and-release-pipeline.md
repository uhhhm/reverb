# 15: Version compatibility window and iOS release pipeline

**What to build:** A phone that updates later than the desktop keeps syncing (ADR 0004). A declared support window says which older protocol versions each release still serves. Devices report their versions. The phone shows a non-blocking update banner, and a clear message when it falls outside the window. Each release publishes an unsigned IPA and a SideStore/AltStore source JSON, and the docs walk an owner through sideloading.

**Blocked by:** 04, 06

**Status:** ready-for-agent

- [ ] The support window is declared, and the pairing, sync, file, cover, and Delegated request protocols serve every version inside it
- [ ] E2E: a runtime speaking an older protocol version inside the window syncs; one outside it is reported to the owner, not silently ignored
- [ ] The API reports peer versions and the newest known release; iOS shows the update banner and the outside-window message
- [ ] A CI job on a macOS runner builds the unsigned IPA on release and publishes it with a SideStore/AltStore source JSON at a stable URL
- [ ] Docs cover SideStore with a free Apple ID (primary) and self-signing with a paid developer account
