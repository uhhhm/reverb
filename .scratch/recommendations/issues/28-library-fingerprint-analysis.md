# 28: Library fingerprint analysis

**What to build:** A background job that fingerprints library tracks with the bundled model and stores the result per recording. The stored fingerprint is the reduced form (64-128 dimensions) and it replicates, so every device ranks identically even when only one device holds the audio file (ADR 0001).

The model runs as a Python sidecar in the existing `desktop/tools/python` venv, invoked like spotdl and yt-dlp, decoding through the bundled ffmpeg. The Go build stays `CGO_ENABLED=0`.

**Blocked by:** 26

**Status:** needs-info

- [ ] Runs on every supported desktop platform and in server mode; the Docker image already carries Python and ffmpeg
- [ ] Analysis runs in the background at low priority and resumes after a restart or a crash
- [ ] The reduced fingerprint replicates per recording; a device without the file uses the replicated one and never recomputes
- [ ] Spectrogram preprocessing matches Essentia's, verified by a test against recorded Essentia reference outputs
- [ ] Model plus runtime stay within the 100 MB download budget
- [ ] Missing runtime, undecodable audio, or a failed embed degrades to no fingerprint rather than failing the library

## Comments

Open until ticket 26 reports measured sizes, per-track timing, and whether reduction to 64-128 dimensions holds up.

Known risk: onnxruntime may not publish cp314 wheels, and the desktop venv is built from the system python3. Docker pins 3.12 and is unaffected. Ticket 26 checks this.
