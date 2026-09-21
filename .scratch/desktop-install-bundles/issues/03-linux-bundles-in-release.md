# 03: Linux install bundles in the release workflow

**What to build:** Publishing a release attaches `Reverb-linux-amd64.tar.gz` and `Reverb-linux-arm64.tar.gz` beside the existing update zips. Each is built on its native runner with the same build tags as the Linux update payload and smoke-tested before upload. The update zips and the updater's payload contract are unchanged.

**Blocked by:** 02 (Linux install bundle, built and installed locally)

**Status:** ready-for-agent

- [ ] The desktop release workflow fetches the Linux tools and builds the install bundle for amd64 and arm64.
- [ ] Before upload, CI extracts each bundle into a temporary directory and checks every bundled tool resolves from the bundle and runs (version checks), with the tools absent from `PATH`.
- [ ] The tarball's archive name includes the version, or the release notes make the version unambiguous.
- [ ] The publish job attaches both tarballs and its expected-asset-count check matches the new total.
- [ ] The existing update zips are byte-for-byte the same format and still pass their integrity checks.
