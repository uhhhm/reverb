# 01 — One-tap update and source setup from the app

**What to build:** Updating Reverb on the phone and subscribing to its SideStore source each take one tap inside Reverb, instead of switching to SideStore and finding the right screen or pasting a URL.

- The phone's version status also reports the IPA URL of the newer release it already announces (OpenAPI, generated contracts, the release tracker). It is empty whenever `latestVersion` is empty.
- The update banner gains an **Update** button. It opens SideStore's install link (`sidestore://install?url=<IPA URL>`); if SideStore is not installed, AltStore's (`altstore://install?url=…`); otherwise the release page. SideStore installs over the existing bundle, so the phone's data is kept.
- A "Get updates in SideStore" action (Devices, or wherever app info lives) opens `sidestore://source?url=<sourceUrl>` using the source URL the core already reports, with the same AltStore fallback.
- The "open SideStore, else AltStore, else web" launcher is a single helper, reused by the signing-expiry tickets.
- The README's install steps use the one-tap source link alongside the plain URL.

**Blocked by:** None — can start immediately

**Status:** done

- [ ] Version status reports the IPA URL for a newer release with an IPA attached, and an empty value otherwise; `make contracts-check` passes
- [ ] Tapping Update with SideStore installed opens SideStore's install flow for that release's IPA
- [ ] Without SideStore, AltStore's install link is tried, then the release page
- [ ] The source action opens SideStore's add-source confirmation for the Reverb source
- [ ] Dismissing the banner still hides it for that version only
- [ ] Development builds (no newer release) show neither the banner nor an Update button; the source action still works
