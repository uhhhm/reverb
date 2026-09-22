# 05: Windows install bundle

**What to build:** Publishing a release attaches `Reverb-windows-amd64.zip`, a portable folder holding `reverb-desktop.exe`, the bundled tools, the bundled Python launcher layout, and a script that creates a Start Menu shortcut (with the Reverb icon) pointing at the extracted exe. A Windows user unzips it anywhere writable, runs the script once, and finds Reverb in Start search; downloads work without anything installed system-wide. A full installer (Inno Setup/MSI) is out of scope for this ticket.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] The Windows release job assembles the portable folder from the fetched Windows tools and the built exe.
- [x] Before upload, CI extracts the zip and checks that the exe is a GUI application carrying the icon and that every bundled tool resolves from the extracted folder.
- [x] The shortcut script creates a per-user Start Menu shortcut without admin rights; running it again does not duplicate the shortcut, and a documented removal path exists.
- [x] The publish job attaches the bundle and its expected-asset-count check matches the new total.
- [x] Self-update from the extracted folder replaces only the exe and the bundled tools still resolve afterwards.
- [x] The existing Windows update zip still holds exactly the single exe the updater expects.
