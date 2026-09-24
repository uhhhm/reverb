// Package embedded is the Python runner a phone uses: CPython linked into the
// app, running yt-dlp and spotDL as modules on the calling goroutine's thread,
// several at once. ffmpeg, ffprobe and QuickJS, which the tools would start as
// processes, run in-process too, through the _reverb_native module that
// ios/native builds (libreverbnative). launcher.py gives each run its own
// argv, output and cancellation.
//
// It needs cgo, Python's headers and libreverbnative, so it builds only with
// the pyembed tag: gomobile's iOS build, and `make test-pyembed` on a Mac,
// which links the host's Python and the macOS slice of libreverbnative.
package embedded
