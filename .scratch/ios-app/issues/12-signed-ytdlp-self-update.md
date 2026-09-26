# 12: Signed yt-dlp self-update

**What to build:** When YouTube breaks yt-dlp, the phone recovers without a new IPA. The release pipeline signs yt-dlp packages, and the phone downloads, verifies, and activates new ones. An unsigned or badly signed package is never run. A failed activation keeps the previous working package.

**Blocked by:** 10

**Status:** done

- [x] The release pipeline publishes signed yt-dlp packages, and the verification public key is built into the app
- [x] The phone checks for, downloads, verifies, and activates new packages
- [x] Tests: an unsigned, wrongly signed, or tampered package is refused
- [x] Tests: when a new package fails to activate, the previous package stays in use
- [x] The installed yt-dlp version is visible in the app

## Implementation

The release workflow signs a reviewed, hash-pinned pure-Python yt-dlp wheel.
The phone embeds the verification key, checks the channel on launch/foreground
(with a daily throttle), and offers a manual check and installed version in
Devices. Signed manifests and archive hashes are checked before extraction;
activation drains Python jobs and probes imports/extractors/QuickJS. Failed
activation restores the previous package, and restart re-verifies stored
archives with fallback and same-release repair.

The signing secret is generated locally in ignored `.env.ytdlp-signing` with
mode 0600. Deployment still requires installing `YTDLP_SIGNING_KEY` in the
`ytdlp-release` GitHub environment and dispatching the workflow; no release has
been published by this implementation task. See `ios/README.md` for setup and
repeatable end-to-end verification.

Verification and two-axis review evidence are in the local, ignored
`docs/task-reports/ios-ticket12/` directory. Full repository gates encountered
existing intermittent process-name/browser-playback failures; their focused
retries passed. The ticket's signer-to-Python E2E and focused race checks passed.
