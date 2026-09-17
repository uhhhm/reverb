# 10: Downloads mint filenames every platform can store

**What to build:** A track downloaded on any platform lands under a name that every other platform in the household can also store. Today artist and title go into the download output template as literals with only a few characters stripped, so a title carrying `:`, `?`, `*`, `"`, `<`, `>` or `|` produces a path that is perfectly legal on Linux and macOS and impossible to create on Windows. The file replicates everywhere except the one device that cannot write the name, and the owner sees a track that silently never arrives.

Sanitisation has to be unconditional rather than applied only on Windows: the name is minted once, on whichever device runs the download, and then travels to peers that may be any platform. Windows rejects more than the illegal character set — trailing dots and spaces, and reserved device names like `CON`, `NUL` and `COM1`, with or without an extension — so all three classes need covering. Both download adapters build their own output template, so both need the same treatment; a fix in one leaves the other minting unportable names.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] A track whose artist or title contains any Windows-illegal character downloads to a portable filename on Linux, macOS and Windows alike, through either download adapter.
- [x] Trailing dots and spaces, and Windows reserved device names, are handled as well as the illegal character set.
- [x] Sanitisation is unconditional, not gated on the running platform, so a name minted on Linux is storable on a Windows peer.
- [x] The resulting name remains recognisable to the owner: sanitising alters the offending characters without mangling the artist and title beyond recognition, and two distinct tracks do not collapse onto one name.
