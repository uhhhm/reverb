# 15: Version compatibility window and iOS release pipeline

**What to build:** A phone that updates later than the desktop keeps syncing (ADR 0004). A declared support window says which older protocol versions each release still serves. Devices report their versions. The phone shows a non-blocking update banner, and a clear message when it falls outside the window. Each release publishes an unsigned IPA and a SideStore/AltStore source JSON, and the docs walk an owner through sideloading.

**Blocked by:** 04, 06

**Status:** done

- [x] The support window is declared, and the pairing, sync, file, cover, and Delegated request protocols serve every version inside it
- [x] E2E: a runtime speaking an older protocol version inside the window syncs; one outside it is reported to the owner, not silently ignored
- [x] The API reports peer versions and the newest known release; iOS shows the update banner and the outside-window message
- [x] A CI job on a macOS runner builds the unsigned IPA on release and publishes it with a SideStore/AltStore source JSON at a stable URL
- [x] Docs cover SideStore with a free Apple ID (primary) and self-signing with a paid developer account

## Implementation

`internal/p2p/compatibility.go` declares `SupportWindow` (current minor and
the one before) and derives each family's served and offered versions from
it; the libp2p user agent carries the build version. `GET /version` reports
the window, each paired device's version and compatibility with a message
naming which device to update, and on the phone the newest stable release
carrying `Reverb.ipa` (`internal/release`, hourly). iOS shows a dismissible
update banner, an outside-window banner, and versions in Devices.

`release.yml`'s `build-ios` job builds the unsigned IPA on `macos-26`,
attaches it to the release and, for stable releases, publishes
`source.json` (`scripts/ios-source.mjs`) on the `ios-source` channel release.
The archive and IPA packaging were dry-run locally; the job itself runs on the
next published release. `ios/README.md` covers SideStore (free Apple ID),
AltStore and self-signing.

`REVERB_COMPAT_E2E_REPORT=<file> go test ./internal/app -run
TestOlderPhoneProtocolWindow` pairs and syncs a phone speaking only the older
versions, checks the desktop answers every family's oldest version, then
reports a device outside the window.
