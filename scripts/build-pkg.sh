#!/bin/bash
set -euo pipefail

VERSION="${1:?version is required}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/release/$VERSION}"
PACKAGE_ID="${TMUX_GHOSTTY_PACKAGE_ID:-com.guyuanshun.tmux-ghostty}"
INSTALLER_IDENTITY="${APPLE_DEVELOPER_ID_INSTALLER_SIGNING_IDENTITY:-}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-0}"
REQUIRE_NOTARIZATION="${REQUIRE_NOTARIZATION:-0}"
NOTARY_APPLE_ID="${APPLE_NOTARY_APPLE_ID:-}"
NOTARY_PASSWORD="${APPLE_NOTARY_PASSWORD:-}"
NOTARY_TEAM_ID="${APPLE_NOTARY_TEAM_ID:-}"
PKG_VERSION="${VERSION#v}"
PACKAGE_NAME="tmux-ghostty_${VERSION}_darwin_universal.pkg"
ARCHIVE_NAME="tmux-ghostty_${VERSION}_darwin_universal.tar.gz"
PACKAGE_ROOT="$DIST_DIR/package-root"
UNSIGNED_PKG="$DIST_DIR/tmux-ghostty-unsigned.pkg"
FINAL_PKG="$DIST_DIR/$PACKAGE_NAME"

export COPYFILE_DISABLE=1

if [[ ! -f "$DIST_DIR/universal/tmux-ghostty" || ! -f "$DIST_DIR/universal/tmux-ghostty-broker" ]]; then
  echo "universal binaries are missing under $DIST_DIR/universal; run scripts/build-release.sh first" >&2
  exit 1
fi
if [[ ! -f "$DIST_DIR/$ARCHIVE_NAME" ]]; then
  echo "archive $ARCHIVE_NAME is missing under $DIST_DIR; run scripts/build-release.sh first" >&2
  exit 1
fi

rm -rf "$PACKAGE_ROOT" "$UNSIGNED_PKG" "$FINAL_PKG"
mkdir -p "$PACKAGE_ROOT/usr/local/bin"
cp "$DIST_DIR/universal/tmux-ghostty" "$PACKAGE_ROOT/usr/local/bin/tmux-ghostty"
cp "$DIST_DIR/universal/tmux-ghostty-broker" "$PACKAGE_ROOT/usr/local/bin/tmux-ghostty-broker"
find "$PACKAGE_ROOT" -name '._*' -delete
xattr -cr "$PACKAGE_ROOT" 2>/dev/null || true
dot_clean -m "$PACKAGE_ROOT" 2>/dev/null || true

pkgbuild \
  --root "$PACKAGE_ROOT" \
  --identifier "$PACKAGE_ID" \
  --version "$PKG_VERSION" \
  --install-location "/" \
  --filter '(^|/)\.DS_Store$' \
  --filter '(^|/)\._' \
  "$UNSIGNED_PKG"

if [[ -n "$INSTALLER_IDENTITY" ]]; then
  productsign --sign "$INSTALLER_IDENTITY" "$UNSIGNED_PKG" "$FINAL_PKG"
elif [[ "$REQUIRE_SIGNING" == "1" ]]; then
  echo "APPLE_DEVELOPER_ID_INSTALLER_SIGNING_IDENTITY is required when REQUIRE_SIGNING=1" >&2
  exit 1
else
  cp "$UNSIGNED_PKG" "$FINAL_PKG"
fi

if [[ -n "$NOTARY_APPLE_ID" && -n "$NOTARY_PASSWORD" && -n "$NOTARY_TEAM_ID" ]]; then
  xcrun notarytool submit "$FINAL_PKG" \
    --apple-id "$NOTARY_APPLE_ID" \
    --password "$NOTARY_PASSWORD" \
    --team-id "$NOTARY_TEAM_ID" \
    --wait
  xcrun stapler staple "$FINAL_PKG"
elif [[ "$REQUIRE_NOTARIZATION" == "1" ]]; then
  echo "APPLE_NOTARY_APPLE_ID / APPLE_NOTARY_PASSWORD / APPLE_NOTARY_TEAM_ID are required when REQUIRE_NOTARIZATION=1" >&2
  exit 1
fi

(
  cd "$DIST_DIR"
  shasum -a 256 "$PACKAGE_NAME" "$ARCHIVE_NAME" > checksums.txt
)

echo "created package: $FINAL_PKG"
echo "checksums: $DIST_DIR/checksums.txt"
