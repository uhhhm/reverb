# 18: ListenBrainz upload (opt-in)

**What to build:** A Settings option to connect a ListenBrainz account and upload listens. This unlocks ListenBrainz's collaborative-filtering recommendations for the user, which then feed in as another candidate source. It's off by default, and the setting explains that ListenBrainz listens are public.

**Blocked by:** 06

**Status:** done

- [x] The ListenBrainz token is stored like other secrets and is never logged
- [x] Listens are submitted through the existing scrobble queue, so they retry offline
- [x] Personal ListenBrainz recommendations appear as a candidate source when connected
- [x] Disconnecting stops uploads and removes the source

## Comments

- The scrobble service now serves several providers. A play is queued once per active link, and each row goes to its own provider. An auth failure breaks only that provider's link. ListenBrainz is a `TokenProvider` (`internal/scrobble/listenbrainz`), linked with `PUT /scrobble/listenbrainz {token}`. The token is validated, then stored in `scrobble_link.session_key`. It is sent only in the `Authorization` header and never appears in errors.
- Personal recommendations (`listenbrainz.Personal`, `/1/cf/recommendation/user/{name}/recording` plus recording metadata) feed Discover Weekly only, with reason `personal`. They come from the service rather than from synced inputs, so only devices connected to the account get them.
- Disconnecting deletes pending uploads and then the link, and regenerates Discover Weekly without the source. A refresh already running reruns afterwards. Reading a Mix also drops `personal` tracks while no account is connected, which covers a Mix kept offline and a link that broke. A batch the worker has already started sending can still go out.
- The scrobble endpoints are not in OpenAPI, so the two new ones aren't either. Only the reason enum changed there.
