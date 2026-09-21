# iOS distribution via sideloading

Reverb publishes an unsigned IPA with each GitHub release, and each owner signs it
themselves. It is not on the App Store: downloading from YouTube and Spotify would
not pass review, and an EU alternative marketplace still requires Apple
notarization.

The documented path is SideStore or AltStore with a free Apple ID. A free signature
lasts 7 days, and those tools re-sign on a schedule, SideStore without a computer.
The release CI publishes a SideStore/AltStore source JSON beside the IPA, so a new
version appears inside the tool rather than requiring a manual download. A paid
developer account, which signs for a year, works with the same IPA and is the
documented alternative.

Re-signing does not update the app, so a phone runs an older version than a
desktop device, which updates itself, for longer than desktops ever lag each other.
Sync, pairing, and Delegated request protocols therefore stay backward compatible
across a declared window of releases, using the version already in each libp2p
protocol ID. The phone shows a non-blocking banner when a newer release exists and
links to the source.

## Consequences

- Protocol changes need a compatibility plan covering the support window; refusing
  to sync on a version mismatch is not an option.
- yt-dlp updates independently of the IPA (ADR 0003), so a YouTube breakage does not
  wait for the owner to install a new release.
- Losing the signature (a missed re-sign) stops the app from launching but keeps its
  data. Deleting the app deletes the phone's database and offline set, which peers
  already hold, except for Downloads still pending upload.
