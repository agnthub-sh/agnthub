BINARY := agnthub
PKG    := ./cmd/$(BINARY)

.PHONY: build install test lint fmt fmt-check cross need-golangci-lint need-node \
	dist dist-release release release-verify release-publish npm-test npm-pack

need-golangci-lint:
	@golangci-lint version 2>/dev/null | grep -q 'version 2\.' || { echo "golangci-lint v2 is required: https://golangci-lint.run/welcome/install/" >&2; exit 1; }

need-node:
	@command -v npm >/dev/null 2>&1 || { echo "Node.js (npm) is required for the npm launcher: https://nodejs.org/" >&2; exit 1; }

build: ## the agnthub CLI into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) $(PKG)

install: ## the agnthub CLI into $(go env GOPATH)/bin
	CGO_ENABLED=0 go install -trimpath $(PKG)

test:
	go test -race ./...

lint: need-golangci-lint
	golangci-lint run ./...

fmt: need-golangci-lint
	golangci-lint fmt ./...

fmt-check: need-golangci-lint
	golangci-lint fmt --diff ./...

cross: ## compile every release platform
	@for os in linux darwin windows; do for arch in amd64 arm64; do \
		echo "$$os/$$arch"; CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o /dev/null $(PKG) || exit 1; \
	done; done

# --- release (archives for linux/darwin/windows × amd64/arm64 + SHA256SUMS) ---

GORELEASER  := go run github.com/goreleaser/goreleaser/v2@v2.19.0
PUBLIC_REPO := agnthub-sh/agnthub
RELEASE_TAG  = $(shell git describe --exact-match --tags --match 'v*' HEAD 2>/dev/null)

dist: ## snapshot archives + SHA256SUMS for every platform into dist/
	$(GORELEASER) release --snapshot --clean

dist-release: ## tagged archives + SHA256SUMS into dist/ (HEAD must carry vX.Y.Z)
	@test -n "$(RELEASE_TAG)" || { echo "HEAD has no vX.Y.Z tag" >&2; exit 1; }
	$(GORELEASER) release --clean

release: need-node dist-release ## dist-release, then embed its checksums in the npm launcher
	node npm/agnthub/scripts/embed-checksums.js dist

release-verify: ## after `make release`: dist/SHA256SUMS must equal the published release's
	@test -n "$(RELEASE_TAG)" || { echo "HEAD has no vX.Y.Z tag" >&2; exit 1; }
	gh release download $(RELEASE_TAG) --repo $(PUBLIC_REPO) --pattern SHA256SUMS --output - | diff - dist/SHA256SUMS

release-publish: ## manual fallback for the release workflow: upload dist/ to the GitHub Release for the HEAD tag
	@test -n "$(RELEASE_TAG)" || { echo "HEAD has no vX.Y.Z tag" >&2; exit 1; }
	gh release create $(RELEASE_TAG) --repo $(PUBLIC_REPO) --verify-tag --title "agnthub $(RELEASE_TAG)" \
		--notes "Verify downloads with \`sha256sum --ignore-missing -c SHA256SUMS\`. Install instructions: https://github.com/$(PUBLIC_REPO)#install" \
		dist/*.tar.gz dist/*.zip dist/SHA256SUMS

npm-test: need-node ## npm launcher tests
	cd npm/agnthub && node --test test/*.test.js

npm-pack: need-node ## npm launcher tarball (after `make release`) into dist/
	cd npm/agnthub && npm pack --pack-destination ../../dist
