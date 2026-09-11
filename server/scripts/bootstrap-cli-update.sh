#!/usr/bin/env bash
# bootstrap-cli-update.sh вЂ” safe CLI-first path for legacy nyxveilctl updaters
#
# Replaces ONLY /usr/local/sbin/nyxveilctl from a GitHub Release, then
# optionally runs `nyxveilctl update` with the fixed updater.
#
# NEVER touches: nyxveil-server, TLS, server.json, nftables, node identity.
# Fail-closed: bad hash / arch / version в†’ old CLI remains intact.
#
# Trust model:
#   1) Download release-manifest-linux-${arch}.json from GitHub Release
#   2) Parse unsigned manifest (version/arch/assets)
#   3) Download nyxveilctl asset; verify SHA-256 from manifest
#   4) Atomic install (temp + fsync + rename), mode 0755, root:root
#
# Usage (production):
#   sudo bash bootstrap-cli-update.sh --version 1.1.13 --then-update
#
# Offline:
#   sudo bash bootstrap-cli-update.sh --manifest /path/manifest.json \
#     --ctl-file /path/nyxveilctl-linux-amd64 --then-update
set -euo pipefail

readonly GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"

VERSION="${NYXVEIL_BOOTSTRAP_VERSION:-1.1.13}"
BIN_DIR="${NYXVEIL_BIN_DIR:-/usr/local/sbin}"
CTL_DEST="${BIN_DIR}/nyxveilctl"
THEN_UPDATE=0
MANIFEST_FILE=""
CTL_FILE=""
BASE_URL=""
WORK=""

die() { echo "bootstrap-cli: $*" >&2; exit 1; }
log() { echo "bootstrap-cli: $*"; }

usage() {
  cat <<'EOF'
Usage: bootstrap-cli-update.sh [options]

  --version X.Y.Z     Target release version (default 1.1.13)
  --then-update       After CLI replace, exec: nyxveilctl update
  --manifest PATH     Use local unsigned manifest (skip download)
  --ctl-file PATH     Use local nyxveilctl binary (skip download)
  --bin-dir DIR       Install directory (default /usr/local/sbin)
  -h|--help           This help

Does NOT stop the server or modify TLS/config.
Trust: GitHub Release HTTPS + SHA-256 integrity (no Ed25519 release signing).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --then-update) THEN_UPDATE=1; shift ;;
    --manifest) MANIFEST_FILE="${2:-}"; shift 2 ;;
    --ctl-file) CTL_FILE="${2:-}"; shift 2 ;;
    --bin-dir) BIN_DIR="${2:-}"; CTL_DEST="${BIN_DIR}/nyxveilctl"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

command -v jq >/dev/null 2>&1 || die "jq required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum required"
command -v curl >/dev/null 2>&1 || die "curl required"

[[ "$(id -u)" -eq 0 ]] || die "root required"

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported arch ${ARCH}" ;;
esac
MANIFEST_ARCH="linux/${ARCH}"
BASE_URL="${NYXVEIL_RELEASE_BASE_URL:-https://github.com/${GITHUB_REPO}/releases/download/server-v${VERSION}}"
if [[ "${NYXVEIL_TEST_MODE:-0}" != 1 ]]; then
  [[ "${GITHUB_REPO}" == Moroz1212/Nyxveil && "${BASE_URL}" == "https://github.com/Moroz1212/Nyxveil/releases/download/server-v${VERSION}" ]] || die "production release source is fixed"
fi

WORK="$(mktemp -d /tmp/nyxveil-bootstrap-cli.XXXXXX)"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

# --- fetch / verify manifest -------------------------------------------------
MANIFEST="${WORK}/release-manifest-linux-${ARCH}.json"
if [[ -n "${MANIFEST_FILE}" ]]; then
  cp -a "${MANIFEST_FILE}" "${MANIFEST}"
else
  log "fetching ${BASE_URL}/release-manifest-linux-${ARCH}.json"
  curl -fsSL --connect-timeout 10 --max-time 120 "${BASE_URL}/release-manifest-linux-${ARCH}.json" -o "${MANIFEST}" \
    || die "manifest download failed"
fi

log "parsed unsigned release manifest (GitHub Release trust)"

MV="$(jq -r '.version' "${MANIFEST}")"
MA="$(jq -r '.arch' "${MANIFEST}")"
[[ "${MV}" == "${VERSION}" ]] || die "version mismatch have ${MV} want ${VERSION}"
[[ "${MA}" == "${MANIFEST_ARCH}" ]] || die "arch mismatch have ${MA} want ${MANIFEST_ARCH}"

CTL_NAME="$(jq -r '.assets[] | select(.name=="nyxveilctl" or .name=="ctl") | .name' "${MANIFEST}" | head -n1)"
CTL_SHA="$(jq -r '.assets[] | select(.name=="nyxveilctl" or .name=="ctl") | .sha256' "${MANIFEST}" | head -n1)"
CTL_URL="$(jq -r '.assets[] | select(.name=="nyxveilctl" or .name=="ctl") | .url' "${MANIFEST}" | head -n1)"
[[ -n "${CTL_NAME}" && -n "${CTL_SHA}" && -n "${CTL_URL}" ]] || die "manifest missing nyxveilctl asset"

# --- fetch / verify ctl binary -----------------------------------------------
NEW_CTL="${WORK}/nyxveilctl.new"
if [[ -n "${CTL_FILE}" ]]; then
  cp -a "${CTL_FILE}" "${NEW_CTL}"
else
  log "fetching nyxveilctl asset"
  if [[ "${NYXVEIL_TEST_MODE:-0}" != 1 ]]; then
    [[ "${CTL_URL}" == "${BASE_URL}/nyxveilctl-linux-${ARCH}" ]] || die "ctl asset must come from the pinned production release"
  fi
  curl -fsSL --connect-timeout 10 --max-time 300 "${CTL_URL}" -o "${NEW_CTL}" || die "ctl download failed (old CLI left intact)"
fi
GOT_SHA="$(sha256sum "${NEW_CTL}" | awk '{print $1}')"
[[ "${GOT_SHA}" == "${CTL_SHA}" ]] || die "sha256 mismatch for nyxveilctl (old CLI left intact)"
chmod 0755 "${NEW_CTL}"

# --- atomic install (temp + fsync + rename); never touch server --------------
mkdir -p "${BIN_DIR}"
TMP_INSTALL="${CTL_DEST}.bootstrap.tmp"
cp -a "${NEW_CTL}" "${TMP_INSTALL}"
chmod 0755 "${TMP_INSTALL}"
chown root:root "${TMP_INSTALL}" 2>/dev/null || true
python3 - <<PY 2>/dev/null || true
import os
p = "${TMP_INSTALL}"
fd = os.open(p, os.O_RDONLY)
os.fsync(fd)
os.close(fd)
dirfd = os.open(os.path.dirname(p) or ".", os.O_RDONLY)
os.fsync(dirfd)
os.close(dirfd)
PY
if [[ -f "${CTL_DEST}" ]]; then
  cp -a "${CTL_DEST}" "${CTL_DEST}.prev" 2>/dev/null || \
    cp -a "${CTL_DEST}" "/var/lib/nyxveil/nyxveilctl.prev" 2>/dev/null || true
fi
mv -f "${TMP_INSTALL}" "${CTL_DEST}"
chmod 0755 "${CTL_DEST}"
chown root:root "${CTL_DEST}" 2>/dev/null || true

log "installed ${CTL_DEST} version target=${VERSION} sha256=${GOT_SHA}"
log "nyxveil-server was NOT modified; TLS/config/identity untouched"

if [[ "${THEN_UPDATE}" -eq 1 ]]; then
  log "running fixed updater: ${CTL_DEST} update"
  exec "${CTL_DEST}" update "${BASE_URL}/release-manifest-linux-${ARCH}.json"
fi

log "next: sudo ${CTL_DEST} update   # or sudo serv_update"
