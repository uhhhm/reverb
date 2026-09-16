# 26: Sound fingerprint prototype

**Type:** prototype

**What to build:** A throwaway spike that answers one question before any product work starts: do Discogs-EffNet embeddings of this library produce nearest neighbours that actually sound alike?

Run outside the app, on the real local library, in a scratch venv. Nothing here ships.

**Blocked by:** none

**Status:** ready-for-human

- [ ] Embeds a few hundred real library tracks with Discogs-EffNet via onnxruntime, decoding with the bundled ffmpeg
- [ ] Prints nearest neighbours for a handful of hand-picked seeds, for human judgement
- [ ] Records measured numbers the later tickets need: model download size, onnxruntime wheel size, seconds to embed one track, and whether cp314 wheels exist on this machine
- [ ] Records whether the embedding survives reduction to 64-128 dimensions with neighbours intact (ticket 28 replicates the reduced form)
- [ ] Writes findings to the ticket and is then deleted

## Comments

Decisions already taken (see ticket 19): Discogs-EffNet, CC BY-NC-SA accepted as non-commercial; a Python sidecar in the bundled venv; fingerprints replicate.
