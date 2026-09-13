# 09: Radio adjusts to skips

**What to build:** During a Radio session, skips steer what comes next. Several skips of the same artist or style push the upcoming queue away from it. Finishing or repeating a track pulls toward it. This comes on top of the long-term taste profile, and only applies to the current session.

**Blocked by:** 08

**Status:** done

- [x] Session adjustments re-rank the upcoming Radio queue without replacing tracks the owner queued manually
- [x] Two skips of the same artist stop that artist for the rest of the session
- [x] Adjustments reset when a new Radio session starts
- [x] Tests cover skip steering and reset

## Comments

- Steering lives in `web/src/lib/radio.ts`. "Style" is approximated by the seed a track was recommended from (its reason): there is no genre data.
