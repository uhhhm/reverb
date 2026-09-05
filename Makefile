.PHONY: gen gen-check test test-go test-web test-race fmt-check vet check check-web check-full setup-web setup-contracts contracts contracts-check contracts-test build web dev clean desktop desktop-dev desktop-deps package-mac

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

check-web:
	cd web && npm run typecheck && npm run lint && npm run test

check: fmt-check vet test-go check-web gen-check contracts-check contracts-test

check-full: check test-race
	cd web && npm run e2e

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
