.PHONY: gen test build web dev clean desktop desktop-dev desktop-deps

VERSION ?= dev

gen:
	@if command -v sqlc >/dev/null 2>&1; then sqlc generate; else go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate; fi

test:
	go test ./cmd/... ./internal/...
	cd web && npm run test

web:
	cd web && npm install && npm run build
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

desktop-deps: # fetch ffmpeg, navidrome, deno and the spotDL venv into desktop/tools/
	desktop/tools/fetch-ffmpeg.sh
	desktop/tools/fetch-navidrome.sh
	desktop/tools/fetch-deno.sh
	desktop/tools/setup-python-venv.sh

clean:
	rm -rf reverb web/dist internal/api/dist
