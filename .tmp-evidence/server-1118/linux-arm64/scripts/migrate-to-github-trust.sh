#!/usr/bin/env bash
# migrate-to-github-trust.sh — one-shot bridge for nodes still on the legacy
# Ed25519-signed updater that cannot accept unsigned release manifests.
#
# After this succeeds, the node runs the new GitHub-Release trust model and
# subsequent updates go through Control Plane / nyxveilctl update as usual.
#
# Trust:
#   Authenticity = GitHub Release HTTPS
#   Integrity = SHA256SUMS + manifest asset SHA-256
#
# Preserves: node identity, node key, config, TLS, state.
# ONE NODE AT A TIME PER LOCATION — do not take down all healthy nodes
# in a location simultaneously.
#
# Usage:
#   sudo ./migrate-to-github-trust.sh --base-url \
#     https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.1.9
#
# Offline:
#   sudo ./migrate-to-github-trust.sh --local-dir /path/to/dist/release
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIVE_FINAL="${ROOT}/live-final-update.sh"

die() { echo "migrate-to-github-trust: $*" >&2; exit 1; }
log() { echo "migrate-to-github-trust: $*"; }

usage() {
  cat <<'EOF'
Usage: migrate-to-github-trust.sh [options]

  --version X.Y.Z   Target version (passed through to live-final-update)
  --base-url URL    GitHub Release asset base URL (online)
  --local-dir DIR   Flat release directory (offline)
  -h, --help

ONE NODE AT A TIME PER LOCATION.
EOF
}

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --version|--base-url|--local-dir)
      ARGS+=("$1" "${2:-}")
      shift 2
      ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "root required (sudo)"
[[ -f "${LIVE_FINAL}" ]] || die "missing live-final-update.sh beside this script"

# Prefer operator-downloaded SHA256SUMS sibling when migrating from a workdir
# that already holds release assets (manual curl of SHA256SUMS + this script).
if [[ -f "./SHA256SUMS" && -f "./live-final-update.sh" ]]; then
  log "verifying this migrate helper / live-final against local SHA256SUMS"
  tr -d '\r' < ./SHA256SUMS | grep -E ' [*]?live-final-update.sh$' | sha256sum -c - >/dev/null \
    || die "live-final-update.sh SHA256SUMS check failed"
fi

log "starting one-shot migration via live-final-update (GitHub trust + SHA-256)"
log "preserves identity/key/config/TLS; updates binaries + management assets"
bash "${LIVE_FINAL}" "${ARGS[@]}"
log "migration complete — node now uses unsigned GitHub Release manifests"
echo "MIGRATE_TO_GITHUB_TRUST=PASS"
