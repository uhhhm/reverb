# 02 — Signing-expiry banner

**What to build:** Reverb tells the owner before its free-Apple-ID signature runs out, instead of silently refusing to launch after a missed re-sign.

- The app reads the expiry date from the provisioning profile embedded in its own bundle.
- When 2 days or less remain, a non-blocking banner says the signature expires in N days (or today) with a **Refresh in SideStore** button that opens SideStore (AltStore as fallback), using the launcher from 01 if it has landed, or introducing it if not.
- No profile (simulator, App Store-style builds) or a long-lived signature (paid account) shows nothing.
- After a re-sign the banner disappears on the next foreground, since the new profile carries a new date.

**Blocked by:** None — can start immediately

**Status:** done

- [ ] A bundle whose profile expires within 2 days shows the banner with the correct day count
- [ ] A profile expiring later, a missing profile, or an unreadable profile shows no banner and no error
- [ ] Refresh in SideStore opens SideStore, falling back to AltStore
- [ ] The banner coexists with the update and incompatible-device banners without hiding them
