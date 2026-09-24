# 11: Phone Downloads, pending upload, and Add from link

**What to build:** The owner downloads a track or album on the phone, and it plays there at once. It stays on the phone, pending upload, until a paired device confirms it holds the file. After that it is pruned unless it belongs to an offline playlist. Pending uploads are never pruned and appear in sync status. Add from link works from the phone: paste a link or share it from another app, then add the tracks to a playlist or download them.

**Blocked by:** 02, 10

**Status:** done

- [x] Downloads on the phone profile land in the local-files library and play immediately
- [x] E2E: a phone Download stays pending upload with the desktop runtime stopped; after reconnect the desktop fetches it, confirms it holds the file, and the phone prunes it
- [x] E2E: a phone Download that belongs to an offline playlist is kept after upload
- [x] Pending uploads are exposed through the API, shown in sync status, and never pruned, including when storage is full
- [x] Add from link resolves Spotify and YouTube links on the phone, from paste or the iOS share sheet, and can add the result to a playlist and/or download it

**Note from 02:** the offline-set keeper records a file in `offline_file` when it selects it, before any bytes arrive, and prunes recorded files no offline playlist names. A phone Download that lands at the same path with the same content would count as an offline file; pending upload has to exclude its files from that pruning.

## Comments

**Implemented (2026-09-24).**

- *Pending upload.* The download manager's completion hook now carries the downloader's output, and yt-dlp reports the file it wrote (`--print-to-file after_move:filepath`). A phone records it in `pending_upload`. The offline keeper settles pending files after every round: once some paired device's latest manifest lists the content, the file is removed, or handed to the offline set (`offline_file`) when an offline playlist names it. A pending file is never pruned, so the offline set's own pruning skips it too (the note from 02). `GET /pending-uploads` lists them; Devices shows them under "Waiting to upload". Covered by `TestPhoneDownloadStaysUntilTheDesktopHoldsIt` and `TestPhoneDownloadInAnOfflinePlaylistStays` (`internal/app`) and the keeper's unit tests.
- *Downloads.* The phone registers only the downloaders its Python has (`pyrun.Has`); on the iPhone that is yt-dlp, which asks for the source's M4A (AVPlayer opens neither WebM nor Ogg) and never transcodes. Search results and recommendations have a Download action. A phone's Downloads join household browsing through the linked hook, as a desktop's do.
- *Add from link.* The phone uses the existing `/links/resolve` and `/links/add`, now with typed responses in OpenAPI and the iOS client. Its yt-dlp downloads one track at a time by artist and title, so on a phone `linkadd` expands a Spotify album or playlist into its tracks through the search sources, and refuses a Spotify track it cannot name (502). Paste in the Add from link sheet (Playlists toolbar), `reverb://add?url=…`, or the new share extension (`ReverbShare`), which opens that URL, or copies the link when it cannot open the app.
- *Found on the way:* yt-dlp's `--parse-metadata` read a one-word title, artist or album ("Aerodynamic") as a field name and tagged the file `NA`, on the desktop too. A one-word value now carries an empty field that makes it a literal.
- Checked on the iPhone 17 simulator: a Deezer result downloaded as AAC M4A with its thumbnail converted and embedded by the in-process ffmpeg, and it was listed as waiting to upload. A YouTube link handed over through the share sheet and `reverb://add` was resolved, added and downloaded.
