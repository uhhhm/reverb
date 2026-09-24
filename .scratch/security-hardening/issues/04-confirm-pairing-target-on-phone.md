# 04: The phone confirms the target device before a pairing link or QR code pairs it

**What to build:** Opening a `reverb://pair` link on the phone, or scanning a pairing QR code, shows a confirmation before anything is redeemed. It lists the target's peer ID and dial addresses, marks each address as LAN/VPN or public, and offers Pair and Cancel. The phone redeems the payload and trusts the peer only after the owner taps Pair. Today the app redeems any `reverb://pair` URL as soon as it opens. An attacker can therefore send a link, from a web page, a message or an email, that points to their own desktop with a code they minted. One tap makes the attacker's peer a permanently trusted household device. The phone then syncs the household change log to it and relays the attacker's signed changes to the owner's other devices.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Opening a `reverb://pair` link shows the confirmation and leaves the paired-device list unchanged until Pair is tapped.
- [x] Cancel, or dismissing the sheet, discards the payload without dialling the target.
- [x] A scanned QR code goes through the same confirmation.
- [x] If every address in the payload is public (not loopback, private, link-local or CGNAT), the confirmation shows a prominent warning.
- [x] Parsing the pairing payload rejects payloads with more than a small fixed number of addresses.
- [x] Typed-code pairing is unchanged, and the possession proof, code expiry and attempt limiting are unchanged.
- [x] A UI test opens a pairing link and asserts the confirmation appears and no device is paired until Pair is tapped. A Go test covers the address cap.
- [x] `go test ./internal/p2p ./internal/api ./mobile/...` and `make ios-test` pass.

## Comments

`make ios-test` passes except `testDelegatedLibraryBrowseAndPlay`, which fails identically at 1c4d612 before this change (the synced catalogue track never appears in Library); it is tracked separately.
