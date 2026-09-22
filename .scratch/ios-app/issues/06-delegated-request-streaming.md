# 06: Delegated request streaming

**What to build:** On the phone, the owner plays library tracks that are not in the offline set by streaming them from a paired device. A new versioned libp2p protocol carries a Delegated request (see CONTEXT.md) for a track by catalog id. The phone targets the Server if it is reachable, otherwise the paired device reached most recently. When no device is reachable, the track is shown as unavailable instead of failing silently.

**Blocked by:** 04

**Status:** done

- [x] A versioned Delegated request protocol, authenticated by existing peer trust, streams a library track by catalog id through the serving device's library adapter
- [x] The phone profile resolves playback of a non-offline track to a Delegated request, choosing the Server first and otherwise the paired device reached most recently
- [x] The API marks tracks as playable-now or unavailable, based on the offline set and reachable devices
- [x] E2E: the phone runtime streams a non-offline track from the desktop runtime; with the desktop stopped, the same track reports unavailable
- [x] iOS plays streamed tracks and visibly marks unavailable ones
- [x] E2E: an unpaired peer's Delegated request is refused
