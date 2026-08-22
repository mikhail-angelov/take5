# version is derived from git: exact tag if on one, otherwise branch-hash-timestamp
TAG=$(shell git describe --tags --abbrev=0 --exact-match 2>/dev/null)
BRANCH=$(if $(TAG),$(TAG),$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null))
HASH=$(shell git rev-parse --short=7 HEAD 2>/dev/null)
TIMESTAMP=$(shell git log -1 --format=%ct HEAD 2>/dev/null | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S)
GIT_VERSION=$(shell printf "%s-%s-%s" "$(BRANCH)" "$(HASH)" "$(TIMESTAMP)")
VERSION=$(if $(filter --,$(GIT_VERSION)),dev,$(GIT_VERSION)) # fallback when not in a git repo

all: test build

build:
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION) -s -w" -o take5 ./cmd/take5
	@# Go already ad-hoc-signs on macOS by default; --options runtime adds the hardened-runtime
	@# bit on top, since AMFI logs "Unrecoverable CT signature issue" for the plain default and
	@# a real Developer ID isn't available here. No confirmed effect on how aggressively macOS
	@# throttles a Chrome-spawned native-messaging host (see docs/plans/
	@# 2026-08-22-segmented-plate-recording.md's "Observability" notes) — cheap to keep regardless.
	@if [ "$$(uname)" = "Darwin" ]; then codesign --sign - --force --options runtime take5; fi

test:
	go clean -testcache
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@rm -f coverage.out

lint:
	golangci-lint run

# lints only what changed since main: the codebase carries pre-existing lint debt from the
# JS -> Go port (docs/plans/go-port.md), so a full-repo run is not clean yet — see CLAUDE.md.
lint-new:
	golangci-lint run --new-from-rev=main

fmt:
	gofmt -s -w $(shell find . -type f -name "*.go" -not -path "./node_modules/*")

# run before every commit: format, lint the diff, test
prep: fmt lint-new test

version:
	@echo "version: $(VERSION)"

# Zips extension/ for release.yml's "package extension" step and .goreleaser.yaml's
# release.extra_files, so a GitHub Release carries the extension alongside the binary
# archives instead of requiring a full source clone to "Load unpacked" it. Excludes
# private-key.pem (gitignored, never meant to leave this machine) and test/ (dev-only, not
# part of the shipped extension).
# Deliberately not dist/: GoReleaser runs this as a before hook and then refuses to start if
# dist/ is non-empty, so the zip has to land somewhere it does not own.
package-extension:
	@mkdir -p build
	@rm -f build/take5-extension.zip
	cd extension && zip -r -q ../build/take5-extension.zip . \
		-x "private-key.pem" -x "test/*" -x ".DS_Store"

# thin wrappers around the built binary's own subcommands, for the manual test loop —
# `install` here means "register as Chrome's native-messaging host", not `go install`. Voice
# deps are best-effort and never fail this target (the leading `-`): registering the
# native-messaging host is required for the extension to work at all, voice narration isn't.
install: build
	./take5 install
	-./take5 setup-voice

doctor: build
	./take5 doctor

setup-voice: build
	./take5 setup-voice

.PHONY: all build test lint lint-new fmt prep version install doctor setup-voice package-extension
