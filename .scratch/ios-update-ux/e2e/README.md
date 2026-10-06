# E2E evidence: tickets 01, 02, 04

Run on 2026-10-06, iPhone 17 Pro simulator, dev build.

1. **Real profile parse.** `SigningProfile.expiry(of:)` on a real free-Apple-ID
   `embedded.mobileprovision` (from a device build of Reverb) returned
   `2026-09-29T11:39:15Z`, identical to `security cms -D -i <profile> | plutil -extract ExpirationDate raw -`.
2. **Banner in the app.** The simulator build was given an injected CMS-wrapped
   profile expiring at 12:00 UTC two days later, re-signed ad hoc, and launched:
   - `expiry-banner-home.png`: "expires in 2 days" with Refresh, above Home.
   - `expiry-banner-devices-source-action.png`: the banner stays opaque over
     scrolled content; Devices → This iPhone shows **Get updates in SideStore**
     (the dev build's source URL).
   An earlier run with a profile expiring the next day showed "tomorrow". Tapping
   Refresh with neither SideStore nor AltStore installed did nothing and did not crash.
3. **Not verifiable here:** the SideStore/AltStore hand-off itself (neither runs in
   the simulator) and the Update button (dev builds never report a newer release).
   Both link shapes are covered by `ReverbTests/SideloadingTests`, and the tracker's
   IPA URL by `internal/release` tests.

Repeat: `xcodebuild build … -derivedDataPath /tmp/reverb-dd`, write a profile into
`Reverb.app/embedded.mobileprovision`, `codesign -f -s - --deep Reverb.app`, launch.
