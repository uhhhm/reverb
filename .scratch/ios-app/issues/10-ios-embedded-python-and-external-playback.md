# 10: Embedded Python on iOS and external playback

**What to build:** The iOS app embeds Python, QuickJS (yt-dlp's JavaScript runtime), and ffmpeg behind the Python runner. Search results and recommendations outside the library then play on the phone, including on cellular with no peer.

**Blocked by:** 08, 09

**Status:** ready-for-agent

- [ ] iOS Python runner implementation, with yt-dlp, QuickJS, and ffmpeg bundled in the app
- [ ] Confirm yt-dlp supports QuickJS as a JavaScript runtime before building; record the result in the ticket
- [ ] A search result or recommendation outside the library plays on a phone with no paired device reachable
- [ ] Prewarming of upcoming external tracks works on the phone
- [ ] The IPA size increase is measured and recorded in the ticket
