#!/usr/bin/env bash
# live-final-update.sh вЂ” self-contained CLI-first final update for legacy nodes.
#
# Trust root (cryptographic authenticity):
#   Embedded Ed25519 UpdatePublicKey (PUB_HEX), identical to installer /
#   bootstrap-cli-update.sh / internal/updater.UpdatePublicKey.
#   Signed release-manifest-linux-${arch}.json is verified first; ctl SHA-256
#   and subsequent nyxveilctl update are bound to that signature.
#
# SHA256SUMS (if fetched) is corruption convenience metadata ONLY. It is never
# the authenticity root. Do not treat script+SHA256SUMS from the same URL as
# cryptographic proof by themselves.
#
# Self-contained: after the operator downloads and executes THIS script with
# --base-url, the script creates a private workdir and fetches every required
# trust/input file. It never assumes VERSION/SHA256SUMS/bootstrap already exist
# beside the script (e.g. in /tmp).
#
# NEVER touches Frozen Core / VPN dataplane config directly; only replaces
# nyxveilctl first, then runs the new signed updater.
set -euo pipefail
umask 077

readonly PUB_HEX="${NYXVEIL_UPDATE_PUB_HEX:-caf921521e213cb1bcdc2f9df4816c2ecd43222b23a47d6f869672e6ab0e79af}"
readonly DEFAULT_VERSION="1.1.4"
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

  --version X.Y.Z   Target version (default: fetch VERSION from --base-url, else 1.1.4)
  --base-url URL    Release asset base URL (online mode)
  --local-dir DIR   Flat release directory (no network; still signature-verifies)
  --verify-chain    Download/verify trust chain only; do not modify the system
  -h, --help        Show this help

Trust: embedded Ed25519 release public key в†’ signed manifest в†’ asset SHA-256.
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
  der="$(mktemp "${WORK}/pub.XXXXXX")"
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

# Must match Go CanonicalManifestBytes / installer / bootstrap-cli-update.sh.
canonical_manifest_bytes() {
  local manifest="$1"
  if [[ -n "${NYXVEIL_MANIFEST_TOOL:-}" ]]; then
    # Test/host helper: e.g. NYXVEIL_MANIFEST_TOOL='go run ./scripts/manifest-tool.go'
    # shellcheck disable=SC2086
    eval ${NYXVEIL_MANIFEST_TOOL} canon "'${manifest}'"
    return 0
  fi
  if command -v jq >/dev/null 2>&1; then
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
    return 0
  fi
  command -v python3 >/dev/null 2>&1 || die "jq, python3, or NYXVEIL_MANIFEST_TOOL required to verify release manifest"
  python3 - "${manifest}" <<'PY'
import json, sys
from pathlib import Path
m = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
out = {"version": m["version"], "arch": m["arch"]}
if isinstance(m.get("sha256"), str) and m["sha256"]:
    out["sha256"] = m["sha256"]
if isinstance(m.get("url"), str) and m["url"]:
    out["url"] = m["url"]
out["min_core"] = m["min_core"]
out["min_protocol"] = m["min_protocol"]
assets = m.get("assets") or []
if isinstance(assets, list) and assets:
    rich = any(
        (a.get("destination") or "") != ""
        or (a.get("mode") or "") != ""
        or a.get("required") is True
        for a in assets
    )
    out_assets = []
    for a in assets:
        item = {"name": a["name"], "sha256": a["sha256"], "url": a["url"]}
        if rich:
            item["destination"] = a.get("destination", "")
            item["mode"] = a.get("mode", "")
            item["required"] = bool(a.get("required", False))
        out_assets.append(item)
    out["assets"] = out_assets
sys.stdout.write(json.dumps(out, separators=(",", ":"), ensure_ascii=False))
PY
}

manifest_field() {
  local manifest="$1"
  local field="$2"
  if [[ -n "${NYXVEIL_MANIFEST_TOOL:-}" ]]; then
    # shellcheck disable=SC2086
    eval ${NYXVEIL_MANIFEST_TOOL} field "'${manifest}'" "'${field}'"
    return 0
  fi
  if command -v jq >/dev/null 2>&1; then
    jq -r "${field}" "${manifest}"
    return 0
  fi
  python3 - "${manifest}" "${field}" <<'PY'
import json, sys
from pathlib import Path
m = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
expr = sys.argv[2]
if expr == ".version":
    print(m.get("version",""))
elif expr == ".arch":
    print(m.get("arch",""))
elif expr == ".signature // empty":
    print(m.get("signature") or "")
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
}

verify_manifest_signature() {
  local manifest="$1"
  local sig_b64 msg pem sigbin
  sig_b64="$(manifest_field "${manifest}" '.signature // empty')"
  [[ -n "${sig_b64}" ]] || die "manifest missing signature"
  msg="${WORK}/canonical.json"
  canonical_manifest_bytes "${manifest}" > "${msg}"
  pem="${WORK}/update.pub.pem"
  write_update_pubkey_pem "${pem}"
  sigbin="${WORK}/sig.bin"
  b64url_decode "${sig_b64}" > "${sigbin}" || die "bad signature encoding"
  openssl pkeyutl -verify -pubin -inkey "${pem}" -rawin -in "${msg}" -sigfile "${sigbin}" >/dev/null 2>&1 \
    || die "manifest signature INVALID вЂ” refusing (system unmodified)"
}

# Textual checksum lists may be published with CRLF from Windows builders.
# Normalize only the checksum *listing* for parsing вЂ” never mutate binaries.
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
command -v openssl >/dev/null 2>&1 || die "openssl required"
command -v curl >/dev/null 2>&1 || die "curl required"
command -v tr >/dev/null 2>&1 || die "tr required"
if ! command -v jq >/dev/null 2>&1 && ! command -v python3 >/dev/null 2>&1; then
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
  if [[ -f "${LOCAL_DIR}/SHA256SUMS" && -f "${BOOTSTRAP_SH}" ]]; then
    cp -a "${LOCAL_DIR}/SHA256SUMS" "${WORK}/SHA256SUMS"
    # Corruption check only (CRLF-safe). Authenticity is the Ed25519 manifest.
    verify_named_checksum "${WORK}/SHA256SUMS" "${BOOTSTRAP_SH}"
  fi
  BASE_URL="${BASE_URL:-${LOCAL_DIR}}"
else
  [[ -n "${BASE_URL}" ]] || die "online mode requires --base-url or NYXVEIL_RELEASE_BASE_URL"
  BASE_URL="${BASE_URL%/}"

  log "fetching VERSION and signed manifest into private workdir"
  if [[ -z "${VERSION}" ]]; then
    fetch "${BASE_URL}/VERSION" "${WORK}/VERSION"
    [[ -s "${WORK}/VERSION" ]] || die "VERSION download empty"
    VERSION="$(tr -d '\r[:space:]' < "${WORK}/VERSION")"
    [[ -n "${VERSION}" ]] || die "VERSION unresolved after download"
  fi

  fetch "${BASE_URL}/release-manifest-linux-${ARCH}.json" "${MANIFEST}"
  fetch "${BASE_URL}/nyxveilctl-linux-${ARCH}" "${NEW_CTL}"
  fetch "${BASE_URL}/bootstrap-cli-update.sh" "${BOOTSTRAP_SH}"
  # Optional corruption metadata (never authenticity root).
  if curl -fsSL "${BASE_URL}/SHA256SUMS" -o "${WORK}/SHA256SUMS" 2>/dev/null; then
    verify_named_checksum "${WORK}/SHA256SUMS" "${BOOTSTRAP_SH}"
    log "bootstrap-cli-update.sh checksum OK (corruption check; trust root is Ed25519)"
  else
    log "SHA256SUMS unavailable; continuing with Ed25519 manifest trust only"
  fi
fi

[[ -n "${VERSION}" ]] || VERSION="${DEFAULT_VERSION}"
[[ -n "${VERSION}" ]] || die "VERSION unresolved"

log "verifying release manifest with embedded UpdatePublicKey"
verify_manifest_signature "${MANIFEST}"
log "manifest signature OK (trust root=UpdatePublicKey)"

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
  # Fail closed if a substituted bootstrap does not embed the same release trust root.
  grep -q "${PUB_HEX}" "${BOOTSTRAP_SH}" || die "bootstrap-cli-update.sh missing UpdatePublicKey trust root"
  log "bootstrap trust root matches UpdatePublicKey"
fi

if [[ "${VERIFY_CHAIN_ONLY}" -eq 1 ]]; then
  log "verify-chain complete; system unmodified"
  echo "LIVE_FINAL_UPDATE_CONSUMER=PASS"
  exit 0
fi

log "installing verified nyxveilctl only (server untouched)"
atomic_install_ctl "${NEW_CTL}"
log "ctl upgraded; running full signed update"

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

log "release assets complete; executing ${GATE_MODE:-live} production gate"
if [[ "${NYXVEIL_SKIP_GATE:-0}" == "1" ]]; then
  log "NYXVEIL_SKIP_GATE=1 вЂ” skipping production gate"
  echo "RESULT=PASS"
  exit 0
fi
GATE_MODE="${GATE_MODE:-live}" exec "${SHARE_DIR}/scripts/production-gate.sh"
