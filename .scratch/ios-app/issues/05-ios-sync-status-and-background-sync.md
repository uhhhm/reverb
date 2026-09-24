# 05: Sync status, manual sync, and background sync on iOS

**What to build:** The owner can see whether the phone is current: last sync, sync in progress, failures, and which device was reached. They can sync on demand. The phone syncs when opened, while music plays, and in iOS background refresh windows. There are no keep-alive tricks.

**Blocked by:** 04

**Status:** done

- [x] The sync status screen shows last sync time, in-progress state, failures, and the paired devices with when each was last reached
- [x] A sync-now action runs a sync and reports its result
- [x] Sync runs when the app comes to the foreground and periodically while audio is playing
- [x] A background refresh task is scheduled and runs a bounded sync when iOS grants it
- [x] A paired device can be unpaired from the phone
