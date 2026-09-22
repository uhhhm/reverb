# 02: Offline set on the phone profile

**What to build:** On a phone-profile Device, the owner marks playlists as offline. Their files arrive through the existing P2P file sync into the local-files adapter and play with no peer reachable. The selection stays local to the device and is never synced, as on desktop. Removing a track prunes its file. Storage use is reported, and a full disk stops fetching without evicting anything.

**Blocked by:** 01

**Status:** done

- [x] E2E: a desktop playlist marked offline on the phone runtime has its files fetched, and its tracks stream from the phone runtime with the desktop runtime stopped
- [x] E2E: a track added to that playlist on the desktop arrives on the phone at the next sync, and a removed track's file is pruned
- [x] The phone's offline selection does not replicate to the desktop, and the desktop's does not replicate to the phone
- [x] The API reports offline storage per playlist and in total, plus per-track fetch progress
- [x] E2E: when the storage limit is reached, fetching stops, the API reports a storage-full state, and no existing offline file is removed
