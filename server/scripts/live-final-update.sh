#!/usr/bin/env bash
# live-final-update.sh вЂ” self-contained CLI-first final update for nodes.
#
# Trust model:
#   Authenticity = GitHub repository / GitHub Release (HTTPS download).
#   Integrity = SHA-256 from release-manifest + SHA256SUMS.
#   No Ed25519 release signing keys.
#
# Self-contained: after the operator downloads and executes THIS script with
# --base-url, the script creates a private workdir and fetches every required
# input file. It never assumes VERSION/SHA256SUMS/bootstrap already exist
# beside the script (e.g. in /tmp).
#
# NEVER touches Frozen Core / VPN dataplane config directly; only replaces
# nyxveilctl first, then runs the new updater.
#
# ONE NODE AT A TIME PER LOCATION.
set -euo pipefail
umask 077

readonly DEFAULT_VERSION="1.1.17"
readonly GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"

VERSION=""
BASE_URL="${NYXVEIL_RELEASE_BASE_URL:-${BASE:-}}"
LOCAL_DIR=""
VERIFY_CHAIN_ONLY=0
WORK=""
BIN_DIR="${NYXVEIL_BIN_DIR:-/usr/local/sbin}"
SHARE_DIR="${NYXVEIL_SHARE_DIR:-/usr/local/share/nyxveil}"
STATE_DIR="${NYXVEIL_STATE_DIR:-/var/lib/nyxveil}"
CTL_DEST="${BIN_DIR}/nyxveilctl"

die() { echo "live-final-update: $*" >&2; exit 1; }
log() { echo "live-final-update: $*"; }

usage() {
  cat <<'EOF'
Usage: live-final-update.sh [options]

  --version X.Y.Z   Target version (default: fetch VERSION from --base-url, else 1.1.17)
  --base-url URL    Release asset base URL (online mode)
  --local-dir DIR   Flat release directory (no network; still SHA-256 verifies)
  --verify-chain    Download/verify trust chain only; do not modify the system
  -h, --help        Show this help

Trust: GitHub Release HTTPS в†’ SHA256SUMS / manifest SHA-256 в†’ assets.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --base-url) BASE_URL="${2:-}"; shift 2 ;;
    --local-dir) LOCAL_DIR="${2:-}"; shift 2 ;;
    --verify-chain) VERIFY_CHAIN_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

if [[ "${NYXVEIL_TEST_MODE:-0}" != 1 && -n "${BASE_URL}" ]]; then
  [[ "${BASE_URL}" =~ ^https://github\.com/Moroz1212/Nyxveil/releases/download/server-v[0-9]+\.[0-9]+\.[0-9]+/?$ ]] \
    || die "production source must be a pinned Moroz1212/Nyxveil release (test overrides require NYXVEIL_TEST_MODE=1)"
fi

cleanup() {
  if [[ -n "${WORK}" && -d "${WORK}" ]]; then
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

require_root() {
  if [[ "${NYXVEIL_SKIP_ROOT:-0}" == "1" || "${VERIFY_CHAIN_ONLY}" -eq 1 ]]; then
    return 0
  fi
  [[ "$(id -u)" -eq 0 ]] || die "root required"
}

manifest_field() {
  local manifest="$1"
  local expr="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r "${expr}" "${manifest}"
    return 0
  fi
  if command -v python3 >/dev/null 2>&1 || command -v python >/dev/null 2>&1; then
    local py=python3
    command -v python3 >/dev/null 2>&1 || py=python
    "${py}" - "${manifest}" "${expr}" <<'PY'
import json, sys
from pathlib import Path
m = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
expr = sys.argv[2]
if expr == ".version":
    print(m.get("version",""))
elif expr == ".arch":
    print(m.get("arch",""))
elif "nyxveilctl" in expr:
    for a in m.get("assets") or []:
        if a.get("name") in ("nyxveilctl","ctl"):
            if ".sha256" in expr:
                print(a.get("sha256",""))
            elif ".url" in expr:
                print(a.get("url",""))
            else:
                print(a.get("name",""))
            break
    else:
        print("")
else:
    raise SystemExit(f"unsupported field {expr}")
PY
    return 0
  fi
  if [[ -n "${NYXVEIL_MANIFEST_TOOL:-}" ]]; then
    # Test/host helper: e.g. NYXVEIL_MANIFEST_TOOL='go run -C ... ./scripts/manifest-tool.go'
    # shellcheck disable=SC2086
    eval ${NYXVEIL_MANIFEST_TOOL} field "'${manifest}'" "'${expr}'"
    return 0
  fi
  die "jq, python3, or NYXVEIL_MANIFEST_TOOL required"
}

# Textual checksum lists may be published with CRLF from Windows builders.
checksum_lines() {
  local sums="$1"
  [[ -f "${sums}" ]] || die "missing checksum file: ${sums}"
  tr -d '\r' < "${sums}"
}

verify_named_checksum() {
  local sums="$1"
  local file="$2"
  local name
  name="$(basename "${file}")"
  (
    cd "$(dirname "${file}")"
    checksum_lines "${sums}" | grep -E " [*]?${name}\$" | sha256sum -c - >/dev/null
  ) || die "checksum verification failed for ${name}"
}

fetch() {
  local url="$1"
  local dest="$2"
  curl -fsSL --connect-timeout 5 --max-time 60 "${url}" -o "${dest}" || die "download failed: ${url}"
}

resolve_arch() {
  local arch
  arch="$(uname -m)"
  case "${arch}" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture ${arch}" ;;
  esac
  MANIFEST_ARCH="linux/${ARCH}"
}

atomic_install_ctl() {
  local src="$1"
  mkdir -p "${BIN_DIR}" "${STATE_DIR}"
  local tmp="${CTL_DEST}.live-final.tmp"
  cp -a "${src}" "${tmp}"
  chmod 0755 "${tmp}"
  chown root:root "${tmp}" 2>/dev/null || true
  if [[ -f "${CTL_DEST}" ]]; then
    cp -a "${CTL_DEST}" "${CTL_DEST}.prev" 2>/dev/null || \
      cp -a "${CTL_DEST}" "${STATE_DIR}/nyxveilctl.prev" 2>/dev/null || true
  fi
  mv -f "${tmp}" "${CTL_DEST}"
  chmod 0755 "${CTL_DEST}"
  chown root:root "${CTL_DEST}" 2>/dev/null || true
}

require_root
command -v sha256sum >/dev/null 2>&1 || die "sha256sum required"
command -v grep >/dev/null 2>&1 || die "grep required"
command -v curl >/dev/null 2>&1 || die "curl required"
command -v tr >/dev/null 2>&1 || die "tr required"
if ! command -v jq >/dev/null 2>&1 \
  && ! command -v python3 >/dev/null 2>&1 \
  && ! command -v python >/dev/null 2>&1 \
  && [[ -z "${NYXVEIL_MANIFEST_TOOL:-}" ]]; then
  die "jq or python3 required"
fi

resolve_arch
WORK="$(mktemp -d "${TMPDIR:-/tmp}/nyxveil-live-update.XXXXXX")"
MANIFEST="${WORK}/release-manifest-linux-${ARCH}.json"
NEW_CTL="${WORK}/nyxveilctl.new"
BOOTSTRAP_SH="${WORK}/bootstrap-cli-update.sh"

if [[ -n "${LOCAL_DIR}" ]]; then
  LOCAL_DIR="$(cd "${LOCAL_DIR}" && pwd)"
  [[ -f "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json" ]] || die "missing architecture manifest"
  [[ -f "${LOCAL_DIR}/nyxveilctl-linux-${ARCH}" ]] || die "missing architecture ctl"
  if [[ -z "${VERSION}" ]]; then
    [[ -f "${LOCAL_DIR}/VERSION" ]] || die "missing VERSION in local release dir"
    VERSION="$(tr -d '\r[:space:]' < "${LOCAL_DIR}/VERSION")"
  fi
  [[ -n "${VERSION}" ]] || die "empty VERSION"
  cp -a "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json" "${MANIFEST}"
  cp -a "${LOCAL_DIR}/nyxveilctl-linux-${ARCH}" "${NEW_CTL}"
  if [[ -f "${LOCAL_DIR}/bootstrap-cli-update.sh" ]]; then
    cp -a "${LOCAL_DIR}/bootstrap-cli-update.sh" "${BOOTSTRAP_SH}"
  fi
  if [[ -f "${LOCAL_DIR}/SHA256SUMS" ]]; then
    cp -a "${LOCAL_DIR}/SHA256SUMS" "${WORK}/SHA256SUMS"
    verify_named_checksum "${WORK}/SHA256SUMS" "${MANIFEST}"
    if [[ -f "${BOOTSTRAP_SH}" ]]; then
      verify_named_checksum "${WORK}/SHA256SUMS" "${BOOTSTRAP_SH}"
    fi
  fi
  BASE_URL="${BASE_URL:-${LOCAL_DIR}}"
else
  [[ -n "${BASE_URL}" ]] || die "online mode requires --base-url or NYXVEIL_RELEASE_BASE_URL"
  BASE_URL="${BASE_URL%/}"

  log "fetching VERSION, SHA256SUMS, and unsigned manifest into private workdir"
  if [[ -z "${VERSION}" ]]; then
    fetch "${BASE_URL}/VERSION" "${WORK}/VERSION"
    [[ -s "${WORK}/VERSION" ]] || die "VERSION download empty"
    VERSION="$(tr -d '\r[:space:]' < "${WORK}/VERSION")"
    [[ -n "${VERSION}" ]] || die "VERSION unresolved after download"
  fi

  fetch "${BASE_URL}/SHA256SUMS" "${WORK}/SHA256SUMS"
  fetch "${BASE_URL}/release-manifest-linux-${ARCH}.json" "${MANIFEST}"
  fetch "${BASE_URL}/nyxveilctl-linux-${ARCH}" "${NEW_CTL}"
  fetch "${BASE_URL}/bootstrap-cli-update.sh" "${BOOTSTRAP_SH}"
  verify_named_checksum "${WORK}/SHA256SUMS" "${MANIFEST}"
  verify_named_checksum "${WORK}/SHA256SUMS" "${BOOTSTRAP_SH}"
  log "SHA256SUMS integrity OK (GitHub Release authenticity)"
fi

[[ -n "${VERSION}" ]] || VERSION="${DEFAULT_VERSION}"
[[ -n "${VERSION}" ]] || die "VERSION unresolved"

log "parsing unsigned release manifest"
MV="$(manifest_field "${MANIFEST}" '.version')"
MA="$(manifest_field "${MANIFEST}" '.arch')"
[[ "${MV}" == "${VERSION}" ]] || die "version mismatch have ${MV} want ${VERSION}"
[[ "${MA}" == "${MANIFEST_ARCH}" ]] || die "arch mismatch have ${MA} want ${MANIFEST_ARCH}"

CTL_SHA="$(manifest_field "${MANIFEST}" '.assets[] | select(.name=="nyxveilctl" or .name=="ctl") | .sha256')"
[[ -n "${CTL_SHA}" ]] || die "manifest missing nyxveilctl asset"
GOT_SHA="$(sha256sum "${NEW_CTL}" | awk '{print $1}')"
[[ "${GOT_SHA}" == "${CTL_SHA}" ]] || die "sha256 mismatch for nyxveilctl (system unmodified)"
chmod 0755 "${NEW_CTL}"
log "ctl asset verified sha256=${GOT_SHA}"

if [[ -f "${BOOTSTRAP_SH}" ]]; then
  chmod 0755 "${BOOTSTRAP_SH}"
fi

if [[ "${VERIFY_CHAIN_ONLY}" -eq 1 ]]; then
  log "verify-chain complete; system unmodified"
  echo "LIVE_FINAL_UPDATE_CONSUMER=PASS"
  exit 0
fi

log "installing verified nyxveilctl only (server untouched)"
atomic_install_ctl "${NEW_CTL}"
log "ctl upgraded; running full update"

export NYXVEIL_BIN_DIR="${BIN_DIR}"
export NYXVEIL_SHARE_DIR="${SHARE_DIR}"
export NYXVEIL_STATE_DIR="${STATE_DIR}"

if [[ -n "${LOCAL_DIR}" ]]; then
  NYXVEIL_UPDATE_LOCAL_DIR="${LOCAL_DIR}" \
    "${CTL_DEST}" update "${LOCAL_DIR}/release-manifest-linux-${ARCH}.json"
else
  "${CTL_DEST}" update "${BASE_URL}/release-manifest-linux-${ARCH}.json"
fi

test -x "${BIN_DIR}/nyxveil-catalog-verify" ||
  die "catalog verifier missing or not executable after update"
test -x "${SHARE_DIR}/scripts/production-gate.sh" ||
  die "production gate missing or not executable after update"
test -f "${SHARE_DIR}/VERSION" || die "share VERSION missing after update"

log "release assets complete; production gate already executed by nyxveilctl update"
if [[ "${NYXVEIL_SKIP_GATE:-0}" == "1" ]]; then
  log "NYXVEIL_SKIP_GATE=1 - update skipped production gate"
  echo "RESULT=PASS"
fi
exit 0
