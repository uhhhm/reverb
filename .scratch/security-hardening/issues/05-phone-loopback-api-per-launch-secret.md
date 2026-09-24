# 05: The phone's loopback API requires a per-launch secret

**What to build:** Another app on the same iPhone can no longer act as the household owner through the phone core's loopback API. Each time the core starts, it generates a random secret. It hands the secret to the Swift layer across the gomobile binding, never over HTTP, and rejects every loopback HTTP and WebSocket request that does not carry it. Today the phone serves the full owner API on a random loopback port with no authentication. Loopback is not a trust boundary on iOS. A malicious app running while Reverb plays in the background can find the port and read the library, playlists and history. It can make edits that the phone signs and replicates to the household, and it can mint or redeem pairing codes to pair a peer it controls. The Spotify search credentials already avoid loopback HTTP for this reason; this ticket extends the same protection to the rest of the API on the phone.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] The phone core rejects a loopback request without the secret, even with no Origin or Referer header, and returns a status that does not reveal which routes exist.
- [x] Comparing the secret takes the same time whether or not it matches.
- [x] The generated Swift client, the WebSocket connection and AVPlayer stream requests send the secret automatically. Playback, seek, sync status, library and playlist editing, and pairing work as before.
- [x] The secret changes on every core start and is never written to disk, logs or the process environment.
- [x] The desktop and server profiles are unchanged: loopback/Host/Origin guards stay as they are and the web SPA needs no secret.
- [x] A Go test starts the phone profile and asserts an owner route is refused without the secret and succeeds with it, and that the pairing-code and QR-redeem routes are among those refused.
- [x] `go test ./mobile/... ./internal/api ./cmd/reverb-testpeer`, `make vet-ios` and `make ios-test` pass.

## Comments

`make ios-test` passes except `testDelegatedLibraryBrowseAndPlay`, which fails identically at 1c4d612 before this change (the synced catalogue track never appears in Library); it is tracked separately.
