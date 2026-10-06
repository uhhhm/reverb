# 03 — Expiry notification before the app stops launching

**What to build:** The owner gets a system notification about a day before Reverb's signature expires, even if they have not opened Reverb, because an expired app cannot run to warn them.

- On each launch and foreground, Reverb schedules (or moves) one local notification for about 24 hours before the profile's expiry date; a re-sign therefore pushes it out automatically.
- Tapping the notification opens SideStore (AltStore as fallback) so the refresh is one tap away.
- Notification permission is requested at a sensible moment, not on first launch before the owner understands why; declining leaves the banner from 02 as the only warning.
- No profile or a long-lived signature schedules nothing and removes any pending reminder.

**Blocked by:** 02 — Signing-expiry banner

**Status:** ready-for-agent

- [ ] Exactly one pending reminder exists, dated ~24 hours before the current profile's expiry
- [ ] A newer profile (after re-sign) replaces the reminder rather than adding a second
- [ ] An expiry less than 24 hours away schedules no past-dated reminder; the banner covers it
- [ ] Tapping the notification opens SideStore, falling back to AltStore
- [ ] Denied notification permission produces no error and no repeated prompt
