# 06: Install docs and release notes

**What to build:** Someone landing on a release page knows which asset to download. The desktop README, the root README and the deployment reference describe first install per platform using the install bundles, and state that the `reverb-desktop-<version>-<os>-<arch>.zip` assets are update payloads consumed by the in-app updater, not something to install by hand. Release notes point at the install bundles.

**Blocked by:** 03 (Linux install bundles in the release workflow), 04 (macOS Reverb.app in the release workflow), 05 (Windows install bundle)

**Status:** done

- [x] Each platform's install steps name the exact asset and the steps to install, first launch, update and uninstall.
- [x] Docs describe the update zips as updater payloads and no longer suggest unzipping them to install.
- [x] Generated or templated release notes list the install bundles first.
- [x] Stale claims about installing by unzipping the bare binary are replaced, not appended to.
