# 12: Signed yt-dlp self-update

**What to build:** When YouTube breaks yt-dlp, the phone recovers without a new IPA. The release pipeline signs yt-dlp packages, and the phone downloads, verifies, and activates new ones. An unsigned or badly signed package is never run. A failed activation keeps the previous working package.

**Blocked by:** 10

**Status:** ready-for-agent

- [ ] The release pipeline publishes signed yt-dlp packages, and the verification public key is built into the app
- [ ] The phone checks for, downloads, verifies, and activates new packages
- [ ] Tests: an unsigned, wrongly signed, or tampered package is refused
- [ ] Tests: when a new package fails to activate, the previous package stays in use
- [ ] The installed yt-dlp version is visible in the app
