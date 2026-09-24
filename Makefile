.PHONY: gen gen-check test test-go test-web test-race fmt-check vet vet-windows vet-darwin vet-ios ios-native test-pyembed ios-core ios-project ios-testpeer ios-test check check-web check-full vulncheck recommend-quality setup-web setup-contracts contracts contracts-check contracts-test build web dev clean desktop desktop-windows desktop-dev desktop-deps package-mac package-linux package-windows

VERSION ?= dev
GO_PACKAGES := ./cmd/... ./internal/... ./desktop/... ./mobile/...

gen:
	@if command -v sqlc >/dev/null 2>&1; then sqlc generate; else go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate; fi

# Fails if regenerating changes anything: queries are stale, or hand-edits
# landed inside sqlc-generated files instead of separate ones (see
# internal/store/db/underlying.go).
gen-check: gen
	@git diff --exit-code -- internal/store/db

test: test-go test-web

test-go:
	go test $(GO_PACKAGES)

test-web:
	cd web && npm run test

test-race:
	go test -race $(GO_PACKAGES) -count=1

fmt-check:
	@test -z "$$(gofmt -l $$(git ls-files --cached --others --exclude-standard '*.go'))"

vet:
	go vet $(GO_PACKAGES)

# Windows-only files -- the _windows_test.go guards for the paths and device
# names only Windows refuses -- are invisible to every other check here, so a
# rename can break them while everything local stays green. Cross-compiled vet
# catches that in seconds, without a Windows machine.
vet-windows:
	GOOS=windows go vet $(GO_PACKAGES)

# The same hole on the other side: the macOS updater seam (the .app bundle,
# codesign resealing) and its _darwin_test.go live behind a build tag no Linux
# check compiles, so they can break without a Mac to notice.
vet-darwin:
	GOOS=darwin go vet $(GO_PACKAGES)

# The phone core as iOS compiles it: its whole dependency graph, which has no
# cgo, so it type-checks on Linux with no iOS toolchain (the CC stub only keeps
# cgo from looking for one). gobind then checks the exported surface is one
# gomobile can bind, which otherwise fails only on a Mac.
vet-ios:
	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 CC=/bin/false go vet ./mobile/...
	@out=$$(mktemp -d) && go tool gobind -lang=go,objc -outdir "$$out" ./mobile/reverbcore && rm -rf "$$out"

# iOS app (macOS and Xcode only; see ios/README.md). gomobile and gobind come
# from the version go.mod pins.
IOS_BIN := $(CURDIR)/ios/build/bin
IOS_DESTINATION ?= platform=iOS Simulator,name=iPhone 17

# The core links the embedded Python runner (internal/pyrun/embedded), so it
# needs ios-native's Python headers; the app links Python and libreverbnative.
# Both are arm64 only, so the simulator slice is too.
IOS_PYTHON_HEADERS := $(CURDIR)/ios/build/native/python/Python.xcframework/ios-arm64/Python.framework/Headers

ios-core:
	@test -d "$(IOS_PYTHON_HEADERS)" || { echo "run make ios-native first" >&2; exit 1; }
	GOBIN=$(IOS_BIN) go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
	PATH="$(IOS_BIN):$$PATH" CGO_CPPFLAGS="-I$(IOS_PYTHON_HEADERS)" gomobile bind \
		-target=ios/arm64,iossimulator/arm64 -tags pyembed \
		-ldflags "-X github.com/uhhhm/reverb/mobile/reverbcore.Version=$(VERSION)" \
		-o ios/Frameworks/Reverbcore.xcframework ./mobile/reverbcore

# The embedded download tools (ios/native): CPython for iOS, FFmpeg and
# QuickJS-ng as in-process libraries, and the bundled yt-dlp, all fetched at
# pinned checksums. The first build takes several minutes.
ios-native:
	ios/native/fetch.sh
	ios/native/build.sh iphoneos iphonesimulator

# The embedded Python runner's tests, against this Mac's Python 3.14 and the
# macOS slice of libreverbnative. REVERB_TEST_NETWORK=1 adds a real YouTube
# resolve.
PYEMBED_NATIVE := $(CURDIR)/ios/build/native/macos
test-pyembed:
	@test -f ios/build/native/ffmpeg.version || ios/native/fetch.sh
	ios/native/build.sh macos
	CGO_CFLAGS="$$(python3.14-config --includes)" \
	CGO_LDFLAGS="$$(python3.14-config --embed --ldflags) -L$(PYEMBED_NATIVE) -lreverbnative -lz" \
	REVERB_PYEMBED_HOME="$$(python3.14 -c 'import sys; print(sys.base_prefix)')" \
	REVERB_TEST_SITE_PACKAGES=$(CURDIR)/ios/build/native/app_packages \
		go test -tags pyembed -count=1 ./internal/pyrun/embedded/

ios-project:
	cd ios && xcodegen generate

# The runtime the UI smoke test pairs with, on 127.0.0.1:47300.
ios-testpeer:
	go run ./cmd/reverb-testpeer

ios-test: ios-native ios-core ios-project
	go build -o $(IOS_BIN)/reverb-testpeer ./cmd/reverb-testpeer
	$(IOS_BIN)/reverb-testpeer & peer=$$!; trap 'kill $$peer 2>/dev/null || true' EXIT; \
	cd ios && xcodebuild test -project Reverb.xcodeproj -scheme Reverb \
		-destination '$(IOS_DESTINATION)' -skipPackagePluginValidation

check-web:
	cd web && npm run typecheck && npm run lint && npm run test

# The same gate CI applies: reachable vulnerabilities fail, with the accepted
# exceptions recorded in the script itself.
vulncheck:
	@if command -v govulncheck >/dev/null 2>&1; then scripts/govulncheck.sh; else GOBIN=$$(go env GOPATH)/bin go install golang.org/x/vuln/cmd/govulncheck@v1.6.0 && PATH="$$(go env GOPATH)/bin:$$PATH" scripts/govulncheck.sh; fi

check: fmt-check vet vet-windows vet-darwin vet-ios test-go check-web gen-check contracts-check contracts-test

check-full: check test-race
	cd web && npm run e2e

# Deterministic holdout evaluation against anonymised history and recorded
# recommendation-source responses. A local database must either capture fresh
# responses or explicitly name the matching recorded fixture used for replay.
# Capture a real-history replay with:
# make recommend-quality ARGS="-db /path/reverb.db -record-cache /path/recommend-cache.json"
# Replay it later with:
# make recommend-quality ARGS="-db /path/reverb.db -fixture /path/recommend-cache.json"
recommend-quality:
	go run ./cmd/recommend-quality $(ARGS)

setup-web:
	cd web && npm ci

setup-contracts:
	npm ci --prefix tools/contracts

contracts:
	node tools/contracts/generate.mjs

contracts-check:
	node tools/contracts/generate.mjs --check

contracts-test:
	node tools/contracts/test.mjs

web:
	cd web && npm run build
	rm -rf internal/api/dist
	cp -r web/dist internal/api/dist

build: web
	CGO_ENABLED=0 go build -tags prod -ldflags "-X main.version=$(VERSION)" -o reverb ./cmd/reverb

dev:
	@echo "Run in two shells:"
	@echo "  1) cd web && npm run dev"
	@echo "  2) go run ./cmd/reverb --dev"

# WAILS_TAGS: "production" compiles the real Wails app (without it internal/app
# is a no-op stub). Linux needs webkit2_41 where only webkit2gtk-4.1 is packaged
# (Fedora/Nobara); drop it on distros shipping 4.0.
WAILS_TAGS ?= desktop,production,webkit2_41

desktop: web
	go build -tags $(WAILS_TAGS) -ldflags "-X main.version=$(VERSION)" -o dist/reverb-desktop ./desktop

# Windows. The flags live in the build script, which CI and the release
# workflow run too, so this target cross-compiles exactly what is published.
desktop-windows: web
	bash desktop/build/windows/build.sh "$(VERSION)"

desktop-dev:
	wails dev -projectdir ./desktop

# Distributable Reverb.app + zip in dist/. Bundles its own relocatable Python
# (spotDL/yt-dlp) rather than desktop/tools/python, which only works in-tree.
package-mac:
	VERSION=$(VERSION) desktop/tools/package-mac.sh

# Linux install bundle dist/Reverb-linux-<arch>.tar.gz: the binary, the bundled
# tools and the same relocatable Python, plus a per-user install script. Needs
# the tools from desktop-deps.
package-linux:
	VERSION=$(VERSION) WAILS_TAGS=$(WAILS_TAGS) desktop/tools/package-linux.sh

# Windows portable first-install bundle. Run on Windows after desktop-deps and
# desktop-windows; the release workflow passes its already-built update binary.
package-windows:
	powershell -NoProfile -File desktop/tools/package-windows.ps1 -Binary dist/reverb-desktop.exe -Output dist/Reverb-windows-amd64.zip

desktop-deps: # fetch ffmpeg, navidrome, deno and the spotDL venv into desktop/tools/
	desktop/tools/fetch-ffmpeg.sh
	desktop/tools/fetch-navidrome.sh
	desktop/tools/fetch-deno.sh
	desktop/tools/setup-python-venv.sh

clean:
	rm -rf reverb web/dist internal/api/dist dist
