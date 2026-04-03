#!/bin/bash
set -euo pipefail

: "${APPLE_DEVELOPER_ID_APP_CERT_P12_BASE64:?APPLE_DEVELOPER_ID_APP_CERT_P12_BASE64 is required}"
: "${APPLE_DEVELOPER_ID_APP_CERT_PASSWORD:?APPLE_DEVELOPER_ID_APP_CERT_PASSWORD is required}"
: "${APPLE_DEVELOPER_ID_INSTALLER_CERT_P12_BASE64:?APPLE_DEVELOPER_ID_INSTALLER_CERT_P12_BASE64 is required}"
: "${APPLE_DEVELOPER_ID_INSTALLER_CERT_PASSWORD:?APPLE_DEVELOPER_ID_INSTALLER_CERT_PASSWORD is required}"

KEYCHAIN_NAME="${APPLE_SIGNING_KEYCHAIN_NAME:-build-signing.keychain-db}"
KEYCHAIN_PASSWORD="${APPLE_SIGNING_KEYCHAIN_PASSWORD:-tmux-ghostty-signing}"
TMP_DIR="$(mktemp -d)"
APP_CERT_PATH="$TMP_DIR/app-cert.p12"
INSTALLER_CERT_PATH="$TMP_DIR/installer-cert.p12"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

decode_base64() {
  if base64 --help >/dev/null 2>&1; then
    base64 --decode
  else
    base64 -D
  fi
}

printf '%s' "$APPLE_DEVELOPER_ID_APP_CERT_P12_BASE64" | decode_base64 > "$APP_CERT_PATH"
printf '%s' "$APPLE_DEVELOPER_ID_INSTALLER_CERT_P12_BASE64" | decode_base64 > "$INSTALLER_CERT_PATH"

security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_NAME"
security set-keychain-settings -lut 21600 "$KEYCHAIN_NAME"
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_NAME"
security list-keychains -d user -s "$KEYCHAIN_NAME" login.keychain-db
security default-keychain -d user -s "$KEYCHAIN_NAME"

security import "$APP_CERT_PATH" \
  -k "$KEYCHAIN_NAME" \
  -P "$APPLE_DEVELOPER_ID_APP_CERT_PASSWORD" \
  -T /usr/bin/codesign \
  -T /usr/bin/productsign

security import "$INSTALLER_CERT_PATH" \
  -k "$KEYCHAIN_NAME" \
  -P "$APPLE_DEVELOPER_ID_INSTALLER_CERT_PASSWORD" \
  -T /usr/bin/codesign \
  -T /usr/bin/productsign

security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN_NAME"
security find-identity -v -p codesigning "$KEYCHAIN_NAME"
