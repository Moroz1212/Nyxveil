#!/usr/bin/env bash
# Trusted one-command wrapper for the server-v1.1.2 CLI-first final update.
# Trust starts with this file: verify it against the release SHA256SUMS before
# executing it, as shown in package-release NOTES.txt.
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION=""
BASE_URL="${NYXVEIL_RELEASE_BASE_URL:-${BASE:-}}"
LOCAL_DIR=""
WORK=""

die() { echo "live-final-update: $*" >&2; exit 1; }
log() { echo "live-final-update: $*"; }

usage() {
  cat <<'EOF'
Usage: live-final-update.sh [options]

  --version X.Y.Z   Target version (default: adjacent VERSION or 1.1.2)
  --base-url URL    Verified release asset base URL (online mode)
  --local-dir DIR   Verified flat release directory (no network downloads)
  -h, --help        Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --base-url) BASE_URL="${2:-}"; shift 2 ;;
    --local-dir) LOCAL_DIR="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

if [[ -z "${VERSION}" ]]; then
  VERSION="$(tr -d '[:space:]' < "${SCRIPT_DIR}/VERSION" 2>/dev/null || true)"
  VERSION="${VERSION:-1.1.2}"
fi
[[ "$(id -u)" -eq 0 ]] || die "root required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum required"
command -v grep >/dev/null 2>&1 || die "grep required"

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture ${ARCH}" ;;
esac

if [[ -n "${LOCAL_DIR}" ]]; then
  LOCAL_DIR="$(cd "${LOCAL_DIR}" && pwd)"
  [[ -f "${LOCAL_DIR}/SHA256SUMS" ]] || die "missing ${LOCAL_DIR}/SHA256SUMS"
  [[ -f "${LOCAL_DIR}/bootstrap-cli-update.sh" ]] || die "missing bootstrap-cli-update.sh"
  [[ -f "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json" ]] || die "missing architecture manifest"
  [[ -f "${LOCAL_DIR}/nyxveilctl-linux-${ARCH}" ]] || die "missing architecture ctl"
  log "verifying local release directory"
  (cd "${LOCAL_DIR}" && sha256sum -c SHA256SUMS >/dev/null) ||
    die "local release checksum verification failed"
  chmod 0755 "${LOCAL_DIR}/bootstrap-cli-update.sh"
  bash "${LOCAL_DIR}/bootstrap-cli-update.sh" \
    --version "${VERSION}" \
    --manifest "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json" \
    --ctl-file "${LOCAL_DIR}/nyxveilctl-linux-${ARCH}"
  NYXVEIL_UPDATE_LOCAL_DIR="${LOCAL_DIR}" \
    /usr/local/sbin/nyxveilctl update "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json"
else
  [[ -n "${BASE_URL}" ]] || die "online mode requires --base-url or NYXVEIL_RELEASE_BASE_URL"
  BASE_URL="${BASE_URL%/}"
  command -v curl >/dev/null 2>&1 || die "curl required"
  WORK="$(mktemp -d /tmp/nyxveil-live-final-update.XXXXXX)"
  trap 'rm -rf "${WORK}"' EXIT
  log "fetching bootstrap trust inputs"
  curl -fsSLo "${WORK}/SHA256SUMS" "${BASE_URL}/SHA256SUMS"
  curl -fsSLo "${WORK}/bootstrap-cli-update.sh" "${BASE_URL}/bootstrap-cli-update.sh"
  (
    cd "${WORK}"
    grep ' bootstrap-cli-update.sh$' SHA256SUMS | sha256sum -c - >/dev/null
  ) || die "bootstrap-cli-update.sh checksum verification failed"
  chmod 0755 "${WORK}/bootstrap-cli-update.sh"
  NYXVEIL_RELEASE_BASE_URL="${BASE_URL}" \
    bash "${WORK}/bootstrap-cli-update.sh" --version "${VERSION}" --then-update
fi

test -x /usr/local/sbin/nyxveil-catalog-verify ||
  die "catalog verifier missing or not executable after update"
test -x /usr/local/share/nyxveil/scripts/production-gate.sh ||
  die "production gate missing or not executable after update"

log "release assets complete; executing ${GATE_MODE:-live} production gate"
GATE_MODE="${GATE_MODE:-live}" exec /usr/local/share/nyxveil/scripts/production-gate.sh
