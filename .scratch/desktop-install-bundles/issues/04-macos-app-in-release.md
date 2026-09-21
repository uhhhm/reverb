# 04: macOS Reverb.app in the release workflow

**What to build:** Publishing a release attaches `Reverb-macOS-<arch>.zip` (Intel and Apple Silicon), each holding a full `Reverb.app` with its icon, bundled tools and relocatable Python, built by the existing macOS packaging target and ad-hoc signed. A Mac user can drag it to Applications and open it (right-click → Open the first time, since it is not notarized). The update zips stay as they are.

**Blocked by:** 01 (Shared relocatable Python runtime build)

**Status:** ready-for-agent

- [ ] The release workflow fetches the macOS tools and runs the macOS packaging target on both the Intel and Apple Silicon runners.
- [ ] The packaging target's bundled-tool version checks gate the upload.
- [ ] The publish job attaches both app zips and its expected-asset-count check matches the new total.
- [ ] Self-update of an installed `.app` replaces the binary inside the bundle, relaunches, and the tools in the bundle's resources still resolve.
- [ ] The existing macOS update zips and their codesign step are unaffected.
