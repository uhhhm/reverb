# 19: Sound fingerprints

**What to build:** Closeness in sound becomes a ranking feature, so "sounds like" recommendations can cross genres that listening data misses. This ticket is now only the ranking slice: fingerprinting the library is ticket 28, fingerprinting candidate previews is ticket 29, and a fixture that can measure the result is ticket 27.

**Blocked by:** 27, 28, 29

**Status:** needs-info

- [ ] Sound closeness joins the weighted features in `internal/recommend/rank.go` alongside support, agreement, taste, familiarity and novelty
- [ ] Ranking stays exact and inspectable, so devices holding the same replicated data rank identically (ADR 0001)
- [ ] A candidate with no fingerprint on either side ranks as if the feature were absent, not as if it were distant
- [ ] Recommendations driven by sound say so in their reason line
- [ ] The quality test improves over the ticket 27 baseline, and the new baseline is saved

## Comments

Decisions taken 2026-09-16:

- **Model:** Essentia Discogs-EffNet. Small, music-style trained, good at "sounds like". Its CC BY-NC-SA licence is non-commercial, which is accepted: this rules out commercial distribution of Reverb with the model bundled. OpenL3 is the permissive fallback if that ever changes, at the cost of being generic rather than music-specific. CLAP was rejected as much larger, with its audio-to-text strength unused here.
- **How it runs:** a Python sidecar in the bundled `desktop/tools/python` venv with onnxruntime, invoked like spotdl and yt-dlp, decoding through the bundled ffmpeg. This keeps the Go build `CGO_ENABLED=0` and needs nothing new in the Docker image. Rejected: onnxruntime from Go via purego, which would mean shipping a native library per platform, and a custom compiled binary per platform, which is the most CI work.
- **Download budget:** 100 MB for model plus runtime, most of it runtime.
- **Fingerprints replicate.** They are derived from local audio files, which are not replicated, so ranking from a locally computed fingerprint would break the ticket 08 guarantee that devices with the same data rank identically. The reduced 64-128 dimension fingerprint is therefore replicated per recording, computed once by whichever device holds the file.

Two problems found in the original ticket, now split out:

- The quality criterion was unmeasurable: Radio already scores 1.0 on the fixture, so nothing can beat it. Ticket 27.
- The ADR 0001 conflict above, which drives the replication decision. Ticket 28.
