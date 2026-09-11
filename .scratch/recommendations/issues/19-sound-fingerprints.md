# 19: Sound fingerprints

**What to build:** A bundled audio model produces a sound fingerprint for each library track and for Deezer 30-second previews of candidates. Closeness in sound (mood, energy, texture) becomes a ranking feature and lets "sounds like" recommendations cross genres that listening data misses.

**Blocked by:** 08

**Status:** ready-for-human (needs decisions: which model and licence, e.g. Essentia Discogs-EffNet or CLAP; how it runs, e.g. an ONNX runtime in Go or a bundled tool like yt-dlp; download size budget)

- [ ] Model runs locally on every supported desktop platform and in server mode
- [ ] Library analysis runs in the background at low priority, can resume, and stores fingerprints per recording
- [ ] Preview fingerprints are cached and never stored as library files
- [ ] The quality test improves over the ticket 08 baseline
