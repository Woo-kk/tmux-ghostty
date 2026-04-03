VERSION ?= dev
DIST_DIR ?= $(CURDIR)/dist/release/$(VERSION)

.PHONY: test build release-binaries package clean

test:
	go test ./...

build:
	go build ./cmd/tmux-ghostty
	go build ./cmd/tmux-ghostty-broker

release-binaries:
	./scripts/build-release.sh $(VERSION)

package: release-binaries
	./scripts/build-pkg.sh $(VERSION)

clean:
	rm -rf dist
