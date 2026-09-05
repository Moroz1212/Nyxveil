#!/usr/bin/env bash
# Remove Nyxveil VPN node packaging. Does NOT flush the host nftables ruleset.
set -euo pipefail

ETC_DIR="/etc/nyxveil"
STATE_DIR="/var/lib/nyxveil"
RUN_DIR="/run/nyxveil"
BIN_DIR="/usr/local/sbin"
SYSCTL_FILE="/etc/sysctl.d/99-nyxveil.conf"
SERVICE_UNIT="/etc/systemd/system/nyxveil-server.service"
NFT_FILE="/etc/nftables.d/nyxveil.conf"

PURGE_STATE=0
PURGE_USER=0

log()  { printf '[nyxveil] %s\n' "$*"; }
warn() { printf '[nyxveil] WARN: %s\n' "$*" >&2; }
die()  { printf '[nyxveil] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: uninstall.sh [--purge-state] [--purge-user]

  Stops the service, removes unit/binaries/sysctl/nftables table inet nyxveil.
  By default keeps /etc/nyxveil and /var/lib/nyxveil (node.key, node_id).

  --purge-state   Also delete /etc/nyxveil and /var/lib/nyxveil
  --purge-user    Also delete system user nyxveil
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge-state) PURGE_STATE=1; shift ;;
    --purge-user) PURGE_USER=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "must run as root"

if command -v systemctl >/dev/null 2>&1; then
  systemctl stop nyxveil-server 2>/dev/null || true
  systemctl disable nyxveil-server 2>/dev/null || true
fi

rm -f "${SERVICE_UNIT}"
systemctl daemon-reload 2>/dev/null || true

# Remove only our isolated table — never flush ruleset.
if command -v nft >/dev/null 2>&1; then
  nft delete table inet nyxveil 2>/dev/null || true
fi
rm -f "${NFT_FILE}"

rm -f "${SYSCTL_FILE}"
sysctl --system >/dev/null 2>&1 || true

rm -f "${BIN_DIR}/nyxveil-server" "${BIN_DIR}/nyxveilctl"
for c in status health start stop restart logs version config update uninstall; do
  rm -f "/usr/local/bin/serv_${c}"
done

rm -rf "${RUN_DIR}"

if [[ "${PURGE_STATE}" -eq 1 ]]; then
  rm -rf "${ETC_DIR}" "${STATE_DIR}"
  log "purged ${ETC_DIR} and ${STATE_DIR}"
else
  warn "kept ${ETC_DIR} and ${STATE_DIR} (use --purge-state to delete node.key)"
fi

if [[ "${PURGE_USER}" -eq 1 ]]; then
  if id -u nyxveil >/dev/null 2>&1; then
    userdel nyxveil 2>/dev/null || warn "could not delete user nyxveil"
  fi
fi

log "uninstall complete"
