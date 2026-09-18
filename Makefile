.PHONY: gen gen-check test test-go test-web test-race fmt-check vet vet-windows check check-web check-full vulncheck recommend-quality setup-web setup-contracts contracts contracts-check contracts-test build web dev clean desktop desktop-windows desktop-dev desktop-deps package-mac

VERSION ?= dev
GO_PACKAGES := ./cmd/... ./internal/... ./desktop/...

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

check-web:
	cd web && npm run typecheck && npm run lint && npm run test

# The same gate CI applies: reachable vulnerabilities fail, with the accepted
# exceptions recorded in the script itself.
vulncheck:
	@if command -v govulncheck >/dev/null 2>&1; then scripts/govulncheck.sh; else GOBIN=$$(go env GOPATH)/bin go install golang.org/x/vuln/cmd/govulncheck@v1.6.0 && PATH="$$(go env GOPATH)/bin:$$PATH" scripts/govulncheck.sh; fi

check: fmt-check vet vet-windows test-go check-web gen-check contracts-check contracts-test

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

desktop-deps: # fetch ffmpeg, navidrome, deno and the spotDL venv into desktop/tools/
	desktop/tools/fetch-ffmpeg.sh
	desktop/tools/fetch-navidrome.sh
	desktop/tools/fetch-deno.sh
	desktop/tools/setup-python-venv.sh

clean:
	rm -rf reverb web/dist internal/api/dist dist
