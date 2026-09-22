# 11: Phone Downloads, pending upload, and Add from link

**What to build:** The owner downloads a track or album on the phone, and it plays there at once. It stays on the phone, pending upload, until a paired device confirms it holds the file. After that it is pruned unless it belongs to an offline playlist. Pending uploads are never pruned and appear in sync status. Add from link works from the phone: paste a link or share it from another app, then add the tracks to a playlist or download them.

**Blocked by:** 02, 10

**Status:** ready-for-agent

- [ ] Downloads on the phone profile land in the local-files library and play immediately
- [ ] E2E: a phone Download stays pending upload with the desktop runtime stopped; after reconnect the desktop fetches it, confirms it holds the file, and the phone prunes it
- [ ] E2E: a phone Download that belongs to an offline playlist is kept after upload
- [ ] Pending uploads are exposed through the API, shown in sync status, and never pruned, including when storage is full
- [ ] Add from link resolves Spotify and YouTube links on the phone, from paste or the iOS share sheet, and can add the result to a playlist and/or download it

**Note from 02:** the offline-set keeper records a file in `offline_file` when it selects it, before any bytes arrive, and prunes recorded files no offline playlist names. A phone Download that lands at the same path with the same content would count as an offline file; pending upload has to exclude its files from that pruning.
