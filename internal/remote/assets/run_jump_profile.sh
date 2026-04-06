#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() {
  echo "error: $*" >&2
  exit 1
}

usage() {
  echo "Usage: run_jump_profile.sh <profile-path> [window-index]" >&2
}

[[ $# -ge 1 ]] || {
  usage
  exit 1
}

PROFILE_PATH="$1"
WINDOW_INDEX="${2:-1}"

[[ -f "$PROFILE_PATH" ]] || die "profile not found: $PROFILE_PATH"

set -a
# shellcheck disable=SC1090
source "$PROFILE_PATH"
set +a

: "${JUMP_HOST:?JUMP_HOST is required}"
: "${JUMP_PORT:?JUMP_PORT is required}"
: "${JUMP_AUTH_MODE:?JUMP_AUTH_MODE is required}"

JUMP_NAME="${JUMP_NAME:-$(basename "$PROFILE_PATH" .env)}"

if [[ -n "${JUMP_USER_FILE:-}" ]]; then
  [[ -f "$JUMP_USER_FILE" ]] || die "JUMP_USER_FILE not found: ${JUMP_USER_FILE}"
  JUMP_USER="$(tr -d '\r' < "${JUMP_USER_FILE}")"
fi

: "${JUMP_USER:?JUMP_USER or JUMP_USER_FILE is required}"

declare -a ssh_cmd
ssh_cmd=(
  ssh
  -tt
  -p "$JUMP_PORT"
  -o StrictHostKeyChecking=accept-new
  -o LogLevel=ERROR
  -o ServerAliveInterval=30
  -o ServerAliveCountMax=3
)

if [[ "${JUMP_AUTH_MODE}" == "key" ]]; then
  [[ -n "${JUMP_IDENTITY_FILE:-}" ]] || die "JUMP_IDENTITY_FILE is required for key auth"
  ssh_cmd+=(-i "$JUMP_IDENTITY_FILE")
fi

if [[ -n "${JUMP_SSH_OPTS:-}" ]]; then
  # Intentional word splitting for user-provided SSH arguments.
  # shellcheck disable=SC2206
  extra_opts=( ${JUMP_SSH_OPTS} )
  ssh_cmd+=("${extra_opts[@]}")
fi

ssh_cmd+=("${JUMP_USER}@${JUMP_HOST}")

echo "[jump] profile=${JUMP_NAME} window=${WINDOW_INDEX} target=${JUMP_USER}@${JUMP_HOST}:${JUMP_PORT}"

case "${JUMP_AUTH_MODE}" in
  password)
    if [[ -n "${JUMP_PASSWORD_FILE:-}" && -f "${JUMP_PASSWORD_FILE:-}" ]] && command -v expect >/dev/null 2>&1; then
      exec "$SCRIPT_DIR/jump_connect.exp" "$JUMP_PASSWORD_FILE" "${ssh_cmd[@]}"
    fi
    echo "[jump] expect or password file unavailable; falling back to manual password entry"
    exec "${ssh_cmd[@]}"
    ;;
  key|manual)
    exec "${ssh_cmd[@]}"
    ;;
  *)
    die "unsupported JUMP_AUTH_MODE: ${JUMP_AUTH_MODE}"
    ;;
esac
