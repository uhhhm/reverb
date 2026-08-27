# DECISIONS — unattended run

Record every call the user did not make. Narrowest reversible option, with alternatives rejected and how to reverse.

---

## D1 — Desktop DB XDG via os.UserConfigDir, not custom env
**Question:** Where to store desktop DB and downloads?
**Taken:** ResolveDesktopDB via os.UserConfigDir + "reverb/reverb.db" (XDG on linux, Application Support on mac), fallback ./data/reverb.db; MaybeMigrateLegacyDB copies if needed. Download dir ~/Music/Reverb via UserHomeDir.
**Rejected:** Custom REVERB_DESKTOP_DATA_DIR env, or reusing ./data directly (would clash with Docker).
**Reverse:** Edit internal/desktop/paths.go; remove env wiring in desktop/main.go.
**Task:** T1

## D2 — Desktop build tag isolates SPA embed, desktop CSP adds wails
**Question:** How to keep web+Docker working while Wails serves SPA?
**Taken:** //go:build desktop for embed_desktop.go returning NotFoundHandler; prod && !desktop and !prod && !desktop for existing embeds; Deps.Desktop bool skips Vite proxy and switches CSP to include wails: http://localhost:* ws://localhost:*; realtime.ts honors window.__REVERB_PORT__.
**Rejected:** Separate server binary, or hash router changes.
**Reverse:** Remove Desktop flag, revert build tags.
**Task:** T2

## D3 — Wails scaffold needs doc.go for vet without tag
**Question:** `go vet ./desktop/...` without tag matched no packages because only file was `//go:build desktop`.
**Taken:** Add desktop/doc.go //go:build !desktop trivial file so vet without tag has a package; frontend.go retains desktop tag but without main.
**Rejected:** Remove build tag from frontend.go, or require `go vet -tags desktop` only.
**Reverse:** Delete doc.go and change vet command to always use -tags desktop.
**Task:** T3

## D4 — Fix duplicate main between frontend.go and main.go
**Question:** frontend.go (desktop) had func main() conflicting with real main.go when made desktop-tagged.
**Taken:** Remove main from frontend.go (replace with var desktopFrontend), remove build tag from main.go (unconditional) so only one main exists both with and without desktop tag.
**Rejected:** Keep both mains with different build constraints (would require keeping main.go !desktop which left desktop binary as stub).
**Reverse:** Restore frontend.go main and make main.go !desktop.
**Task:** T4

## D5 — Single-instance via O_EXCL not flock
**Question:** How to enforce single instance on DataDir/lock?
**Taken:** Use O_CREATE|O_EXCL file creation plus in-process map guard; simple portable, no syscall.Flock build tags needed.
**Rejected:** syscall.Flock (needs windows build tag, auto-releases on crash — nicer but more complexity).
**Reverse:** Replace AcquireSingleInstanceLock with flock-based implementation.
**Task:** T5

## D6 — .gitignore narrowed from desktop/tools/ to bin/python
**Question:** T3 added desktop/tools/ which ignored fetch scripts themselves.
**Taken:** Narrow to desktop/tools/bin/ and desktop/tools/python/, keep fetch-*.sh tracked, binaries ignored.
**Rejected:** Keep broad ignore and git add -f scripts, or add Negation ! pattern.
**Reverse:** Edit .gitignore back to broad.
**Task:** T5

## D7 — Auto-update polling placeholder, no go-selfupdate dep yet
**Question:** Spec D4 requires go-selfupdate polling and Apply; adding dep adds CGO/network weight.
**Taken:** Implement LatestRelease/PickAsset/CheckAndEmit/StartPollers and ytdlp upgrade via ExecCommand, with TODO for Apply; no new go.mod dep, keep gate pure Go. Pollers helper provided for App to call later.
**Rejected:** Add creativeprojects/go-selfupdate now (would require go get and increase binary, but doable).
**Reverse:** go get github.com/creativeprojects/go-selfupdate and wire CheckUpdate/ApplyUpdate in App.
**Task:** T6

## D8 — close→quit per D3, no tray
**Question:** Spec D3 close→quit vs hide to tray.
**Taken:** OnBeforeClose returns false (allow quit), per spec. Documented that switch to Hide is one line.
**Rejected:** Tray persistence.
**Reverse:** Change OnBeforeClose to WindowHide.
**Task:** T4/T5
