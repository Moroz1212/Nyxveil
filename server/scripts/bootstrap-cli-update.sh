#!/usr/bin/env bash
# bootstrap-cli-update.sh вЂ” safe CLI-first path for legacy nyxveilctl updaters
#
# Replaces ONLY /usr/local/sbin/nyxveilctl with a signed release asset, then
# optionally runs `nyxveilctl update` with the FIXED updater.
#
# NEVER touches: nyxveil-server, TLS, server.json, nftables, node identity.
# Fail-closed: bad signature / hash / arch / version в†’ old CLI remains intact.
#
# Trust model (same as installer / Go updater):
#   1) Download release-manifest-linux-${arch}.json
#   2) Verify Ed25519 signature (UpdatePublicKey / PUB_HEX)
#   3) Download nyxveilctl asset; verify SHA-256
#   4) Atomic install (temp + fsync + rename), mode 0755, root:root
#
# Usage (production, from server-v1.1.1):
#   sudo bash bootstrap-cli-update.sh --version 1.1.5 --then-update
#
# Offline:
#   sudo bash bootstrap-cli-update.sh --manifest /path/manifest.json \
#     --ctl-file /path/nyxveilctl-linux-amd64 --then-update
set -euo pipefail

readonly PUB_HEX="caf921521e213cb1bcdc2f9df4816c2ecd43222b23a47d6f869672e6ab0e79af"
readonly GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"

VERSION="${NYXVEIL_BOOTSTRAP_VERSION:-1.1.5}"
BIN_DIR="${NYXVEIL_BIN_DIR:-/usr/local/sbin}"
CTL_DEST="${BIN_DIR}/nyxveilctl"
THEN_UPDATE=0
MANIFEST_FILE=""
CTL_FILE=""
BASE_URL=""
WORK=""
DUMP_CANONICAL=""

die() { echo "bootstrap-cli: $*" >&2; exit 1; }
log() { echo "bootstrap-cli: $*"; }

usage() {
  cat <<'EOF'
Usage: bootstrap-cli-update.sh [options]

  --version X.Y.Z     Target release version (default 1.1.5)
  --then-update       After CLI replace, exec: nyxveilctl update
  --manifest PATH     Use local signed manifest (skip download)
  --ctl-file PATH     Use local nyxveilctl binary (skip download)
  --bin-dir DIR       Install directory (default /usr/local/sbin)
  -h|--help           This help

Does NOT stop the server or modify TLS/config.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --then-update) THEN_UPDATE=1; shift ;;
    --manifest) MANIFEST_FILE="${2:-}"; shift 2 ;;
    --ctl-file) CTL_FILE="${2:-}"; shift 2 ;;
    --bin-dir) BIN_DIR="${2:-}"; CTL_DEST="${BIN_DIR}/nyxveilctl"; shift 2 ;;
    --dump-canonical) DUMP_CANONICAL="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

command -v jq >/dev/null 2>&1 || die "jq required"

hex_to_bin() {
  local hex="$1" i
  for ((i = 0; i < ${#hex}; i += 2)); do
    printf "\\x${hex:i:2}"
  done
}

b64url_decode() {
  local s="$1"
  case $(( ${#s} % 4 )) in
    2) s="${s}==" ;;
    3) s="${s}=" ;;
  esac
  s="$(printf '%s' "${s}" | tr '_-' '/+')"
  printf '%s' "${s}" | base64 -d 2>/dev/null
}

write_update_pubkey_pem() {
  local dest="$1" der
  der="$(mktemp)"
  {
    hex_to_bin "302a300506032b6570032100"
    hex_to_bin "${PUB_HEX}"
  } > "${der}"
  {
    echo "-----BEGIN PUBLIC KEY-----"
    base64 -w 64 "${der}" 2>/dev/null || base64 "${der}" | fold -w 64
    echo "-----END PUBLIC KEY-----"
  } > "${dest}"
  rm -f "${der}"
}

canonical_manifest_bytes() {
  local manifest="$1"
  local canonical
  canonical="$(
    jq -c '
      . as $manifest |
      {
        version: .version,
        arch: .arch
      }
      + (if (.sha256 | type) == "string" and .sha256 != ""
         then {sha256: .sha256}
         else {}
         end)
      + (if (.url | type) == "string" and .url != ""
         then {url: .url}
         else {}
         end)
      + {
        min_core: .min_core,
        min_protocol: .min_protocol
      }
      + (if (.assets | type) == "array" and (.assets | length) > 0
         then {
           assets: [
             .assets[] |
             if ($manifest.assets | any(
               ((.destination // "") != "" or (.mode // "") != "" or (.required // false) == true)
             ))
             then {
                 name: .name,
                 sha256: .sha256,
                 url: .url,
                 destination: .destination,
                 mode: .mode,
                 required: .required
               }
             else {
                 name: .name,
                 sha256: .sha256,
                 url: .url
               }
             end
           ]
         }
         else {}
         end)
    ' "${manifest}"
  )"
  printf '%s' "${canonical}"
}

if [[ -n "${DUMP_CANONICAL}" ]]; then
  [[ -f "${DUMP_CANONICAL}" ]] || die "manifest not found: ${DUMP_CANONICAL}"
  canonical_manifest_bytes "${DUMP_CANONICAL}"
  exit 0
fi

[[ "$(id -u)" -eq 0 ]] || die "root required"

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported arch ${ARCH}" ;;
esac
MANIFEST_ARCH="linux/${ARCH}"
BASE_URL="${NYXVEIL_RELEASE_BASE_URL:-https://github.com/${GITHUB_REPO}/releases/download/server-v${VERSION}}"

command -v openssl >/dev/null 2>&1 || die "openssl required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum required"
command -v curl >/dev/null 2>&1 || die "curl required"

WORK="$(mktemp -d /tmp/nyxveil-bootstrap-cli.XXXXXX)"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

verify_manifest_signature() {
  local manifest="$1"
  local sig_b64 msg pem sigbin
  sig_b64="$(jq -r '.signature // empty' "${manifest}")"
  [[ -n "${sig_b64}" ]] || die "manifest missing signature"
  msg="${WORK}/canonical.json"
  canonical_manifest_bytes "${manifest}" > "${msg}"
  pem="${WORK}/update.pub.pem"
  write_update_pubkey_pem "${pem}"
  sigbin="${WORK}/sig.bin"
  b64url_decode "${sig_b64}" > "${sigbin}" || die "bad signature encoding"
  openssl pkeyutl -verify -pubin -inkey "${pem}" -rawin -in "${msg}" -sigfile "${sigbin}" >/dev/null 2>&1 \
    || die "manifest signature INVALID вЂ” refusing (old CLI left intact)"
}

# --- fetch / verify manifest -------------------------------------------------
MANIFEST="${WORK}/release-manifest-linux-${ARCH}.json"
if [[ -n "${MANIFEST_FILE}" ]]; then
  cp -a "${MANIFEST_FILE}" "${MANIFEST}"
else
  log "fetching ${BASE_URL}/release-manifest-linux-${ARCH}.json"
  curl -fsSL "${BASE_URL}/release-manifest-linux-${ARCH}.json" -o "${MANIFEST}" \
    || die "manifest download failed"
fi

verify_manifest_signature "${MANIFEST}"
log "manifest signature OK"

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
  curl -fsSL "${CTL_URL}" -o "${NEW_CTL}" || die "ctl download failed (old CLI left intact)"
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
# fsync file + parent dir best-effort
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
# Best-effort rollback copy. A failure here does not risk the live CLI: the
# verified replacement is still installed atomically, and rename failure leaves
# the old destination untouched.
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
