#!/bin/bash
set -euo pipefail

VERSION="${1:-dev}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/release/$VERSION}"
RELEASE_REPO="${TMUX_GHOSTTY_RELEASE_REPO:-Woo-kk/tumx-ghostty}"
PACKAGE_ID="${TMUX_GHOSTTY_PACKAGE_ID:-com.guyuanshun.tmux-ghostty}"
SIGNING_IDENTITY="${APPLE_DEVELOPER_ID_APP_SIGNING_IDENTITY:-}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-0}"
COMMIT="${GIT_COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
ARCHIVE_NAME="tmux-ghostty_${VERSION}_darwin_universal.tar.gz"
LDFLAGS="-X github.com/guyuanshun/tmux-ghostty/internal/buildinfo.Version=${VERSION} -X github.com/guyuanshun/tmux-ghostty/internal/buildinfo.Commit=${COMMIT} -X github.com/guyuanshun/tmux-ghostty/internal/buildinfo.BuildDate=${BUILD_DATE} -X github.com/guyuanshun/tmux-ghostty/internal/buildinfo.ReleaseRepo=${RELEASE_REPO} -X github.com/guyuanshun/tmux-ghostty/internal/buildinfo.PackageID=${PACKAGE_ID}"

export COPYFILE_DISABLE=1

mkdir -p "$DIST_DIR/amd64" "$DIST_DIR/arm64" "$DIST_DIR/universal"

build_one() {
  local arch="$1"
  local output_dir="$DIST_DIR/$arch"
  (
    cd "$ROOT_DIR"
    GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$output_dir/tmux-ghostty" ./cmd/tmux-ghostty
    GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$output_dir/tmux-ghostty-broker" ./cmd/tmux-ghostty-broker
  )
}

codesign_if_configured() {
  local path="$1"
  if [[ -n "$SIGNING_IDENTITY" ]]; then
    codesign --force --options runtime --sign "$SIGNING_IDENTITY" "$path"
  elif [[ "$REQUIRE_SIGNING" == "1" ]]; then
    echo "APPLE_DEVELOPER_ID_APP_SIGNING_IDENTITY is required when REQUIRE_SIGNING=1" >&2
    exit 1
  fi
}

build_one amd64
build_one arm64

lipo -create -output "$DIST_DIR/universal/tmux-ghostty" \
  "$DIST_DIR/amd64/tmux-ghostty" \
  "$DIST_DIR/arm64/tmux-ghostty"
lipo -create -output "$DIST_DIR/universal/tmux-ghostty-broker" \
  "$DIST_DIR/amd64/tmux-ghostty-broker" \
  "$DIST_DIR/arm64/tmux-ghostty-broker"

chmod +x "$DIST_DIR/universal/tmux-ghostty" "$DIST_DIR/universal/tmux-ghostty-broker"
codesign_if_configured "$DIST_DIR/universal/tmux-ghostty"
codesign_if_configured "$DIST_DIR/universal/tmux-ghostty-broker"

tar -C "$DIST_DIR/universal" -czf "$DIST_DIR/$ARCHIVE_NAME" tmux-ghostty tmux-ghostty-broker

echo "built release artifacts under $DIST_DIR"
echo "archive: $DIST_DIR/$ARCHIVE_NAME"
