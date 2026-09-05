#!/usr/bin/env bash
# Nyxveil VPN node installer — Ubuntu 24.04 / systemd / nftables.
# Transactional: EXIT trap rolls back until COMMIT.
set -euo pipefail

readonly NYXVEIL_VERSION="${NYXVEIL_VERSION:-1.0.0}"
readonly GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"
readonly ETC_DIR="/etc/nyxveil"
readonly STATE_DIR="/var/lib/nyxveil"
readonly RUN_DIR="/run/nyxveil"
readonly BIN_DIR="/usr/local/sbin"
readonly SYSCTL_FILE="/etc/sysctl.d/99-nyxveil.conf"
readonly SERVICE_UNIT="/etc/systemd/system/nyxveil-server.service"
readonly NFT_FILE="/etc/nftables.d/nyxveil.conf"
readonly CONFIG_FILE="${ETC_DIR}/server.json"
readonly NODE_KEY="${STATE_DIR}/node.key"
readonly DEFAULT_VPN_SUBNET="10.66.0.0/24"
readonly MIN_RAM_MB_WARN=700
readonly MIN_DISK_MB=200

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

COMMITTED=0
CREATED_USER=0
INSTALLED_BINARIES=0
INSTALLED_UNIT=0
INSTALLED_SYSCTL=0
INSTALLED_NFT=0
WROTE_CONFIG=0
STARTED_SERVICE=0
BACKUP_DIR=""
PRESERVE_NODE_ID=""
PRESERVE_HAD_KEY=0

CONTROL_PLANE=""
LOCATION_ID=""
DISPLAY_NAME=""
BOOTSTRAP_TOKEN=""
PUBLIC_HOST=""
PUBLIC_IP=""
TLS_PORT="443"
QUIC_PORT="443"
BINARY_DIR=""
SKIP_DOWNLOAD=0
VPN_SUBNET="${DEFAULT_VPN_SUBNET}"
NONINTERACTIVE=0

log()  { printf '[nyxveil] %s\n' "$*"; }
warn() { printf '[nyxveil] WARN: %s\n' "$*" >&2; }
die()  { printf '[nyxveil] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: install.sh [options]

  --control-plane URL     Control Plane base URL (https://...)
  --location ID           Location ID
  --name NAME             Display name
  --bootstrap-token TOKEN One-time registration token (never written to disk)
  --public-host HOST      Public hostname for endpoints
  --public-ip IP          Public IPv4 (used if --public-host omitted)
  --tls-port PORT         TLS listen port (default 443)
  --quic-port PORT        QUIC listen port (default 443)
  --binary-dir DIR        Local directory with nyxveil-server + nyxveilctl
  --skip-download         Do not fetch release binaries (requires --binary-dir)
  --vpn-subnet CIDR       VPN client subnet (default 10.66.0.0/24)
  --non-interactive       Fail instead of prompting
  -h, --help              Show this help

Examples:
  curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash
  sudo ./install.sh --binary-dir ./dist/linux-amd64 --skip-download
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --control-plane) CONTROL_PLANE="${2:-}"; shift 2 ;;
      --location) LOCATION_ID="${2:-}"; shift 2 ;;
      --name) DISPLAY_NAME="${2:-}"; shift 2 ;;
      --bootstrap-token) BOOTSTRAP_TOKEN="${2:-}"; shift 2 ;;
      --public-host) PUBLIC_HOST="${2:-}"; shift 2 ;;
      --public-ip) PUBLIC_IP="${2:-}"; shift 2 ;;
      --tls-port) TLS_PORT="${2:-}"; shift 2 ;;
      --quic-port) QUIC_PORT="${2:-}"; shift 2 ;;
      --binary-dir) BINARY_DIR="${2:-}"; shift 2 ;;
      --skip-download) SKIP_DOWNLOAD=1; shift ;;
      --vpn-subnet) VPN_SUBNET="${2:-}"; shift 2 ;;
      --non-interactive) NONINTERACTIVE=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "unknown argument: $1" ;;
    esac
  done
}

require_root() {
  [[ "$(id -u)" -eq 0 ]] || die "must run as root (sudo)"
}

check_os() {
  [[ -r /etc/os-release ]] || die "cannot read /etc/os-release"
  # shellcheck source=/dev/null
  . /etc/os-release
  if [[ "${ID:-}" != "ubuntu" ]]; then
    die "Ubuntu required (found ID=${ID:-unknown})"
  fi
  if [[ "${VERSION_ID:-}" != "24.04" ]]; then
    warn "Ubuntu 24.04 recommended (found ${VERSION_ID:-unknown}); continuing"
  fi
}

check_systemd() {
  local pid1
  pid1="$(ps -p 1 -o comm= 2>/dev/null || true)"
  [[ "${pid1}" == "systemd" ]] || die "systemd must be PID 1 (found: ${pid1:-unknown})"
}

detect_arch() {
  local m
  m="$(uname -m)"
  case "${m}" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "unsupported architecture: ${m} (need amd64 or arm64)" ;;
  esac
}

check_tun() {
  if [[ ! -e /dev/net/tun ]]; then
    die "/dev/net/tun missing — enable TUN/TAP (modprobe tun) before installing"
  fi
  if [[ ! -c /dev/net/tun ]]; then
    die "/dev/net/tun is not a character device"
  fi
}

check_resources() {
  local mem_kb mem_mb disk_mb
  mem_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)"
  mem_mb=$((mem_kb / 1024))
  if [[ "${mem_mb}" -lt "${MIN_RAM_MB_WARN}" ]]; then
    warn "RAM ${mem_mb}MB < ${MIN_RAM_MB_WARN}MB — node may be constrained"
  fi
  disk_mb="$(df -Pm /var 2>/dev/null | awk 'NR==2 {print $4}')"
  if [[ -z "${disk_mb}" ]]; then
    disk_mb="$(df -Pm / | awk 'NR==2 {print $4}')"
  fi
  if [[ -n "${disk_mb}" && "${disk_mb}" -lt "${MIN_DISK_MB}" ]]; then
    die "insufficient disk: ${disk_mb}MB free (need >= ${MIN_DISK_MB}MB on /var or /)"
  fi
}

pkg_installed() {
  dpkg -s "$1" >/dev/null 2>&1
}

ensure_packages() {
  local need=()
  local p
  for p in nftables iproute2 ca-certificates curl; do
    if ! pkg_installed "${p}"; then
      need+=("${p}")
    fi
  done
  if [[ ${#need[@]} -eq 0 ]]; then
    log "apt packages already present"
    return 0
  fi
  log "installing packages: ${need[*]}"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends "${need[@]}"
}

prompt_if_empty() {
  local var_name="$1"
  local prompt="$2"
  local secret="${3:-0}"
  local current
  current="$(eval "echo \"\${${var_name}}\"")"
  if [[ -n "${current}" ]]; then
    return 0
  fi
  if [[ "${NONINTERACTIVE}" -eq 1 ]]; then
    die "missing required value: ${var_name} (non-interactive)"
  fi
  # Prefer /dev/tty so "curl | sudo bash" can still prompt interactively.
  local tty_in="/dev/tty"
  if [[ ! -r "${tty_in}" ]]; then
    if [[ -t 0 ]]; then
      tty_in="/dev/stdin"
    else
      die "missing required value: ${var_name} (no TTY; pass flags)"
    fi
  fi
  if [[ "${secret}" -eq 1 ]]; then
    read -r -s -p "${prompt}: " current < "${tty_in}"
    echo
  else
    read -r -p "${prompt}: " current < "${tty_in}"
  fi
  eval "${var_name}=\"\${current}\""
}

gather_inputs() {
  prompt_if_empty CONTROL_PLANE "Control Plane URL (https://...)"
  prompt_if_empty LOCATION_ID "Location ID"
  prompt_if_empty DISPLAY_NAME "Node display name"
  prompt_if_empty BOOTSTRAP_TOKEN "Bootstrap token" 1

  [[ -n "${CONTROL_PLANE}" ]] || die "control plane URL required"
  [[ -n "${LOCATION_ID}" ]] || die "location ID required"
  [[ -n "${DISPLAY_NAME}" ]] || die "display name required"
  [[ -n "${BOOTSTRAP_TOKEN}" ]] || die "bootstrap token required"

  if [[ -z "${PUBLIC_HOST}" && -n "${PUBLIC_IP}" ]]; then
    PUBLIC_HOST="${PUBLIC_IP}"
  fi
  if [[ -z "${PUBLIC_HOST}" ]]; then
    if [[ "${NONINTERACTIVE}" -eq 0 ]]; then
      if [[ -r /dev/tty ]]; then
        read -r -p "Public host or IP (optional): " PUBLIC_HOST < /dev/tty || true
      elif [[ -t 0 ]]; then
        read -r -p "Public host or IP (optional): " PUBLIC_HOST || true
      fi
    fi
  fi

  case "${TLS_PORT}" in
    ''|*[!0-9]*) die "invalid --tls-port" ;;
  esac
  case "${QUIC_PORT}" in
    ''|*[!0-9]*) die "invalid --quic-port" ;;
  esac
}

detect_repair() {
  if [[ -f "${CONFIG_FILE}" ]]; then
    PRESERVE_NODE_ID="$(sed -n 's/.*"node_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${CONFIG_FILE}" | head -n1 || true)"
  fi
  if [[ -f "${NODE_KEY}" ]]; then
    PRESERVE_HAD_KEY=1
    log "repair mode: preserving ${NODE_KEY}"
  fi
  if [[ -n "${PRESERVE_NODE_ID}" ]]; then
    log "repair mode: preserving node_id=${PRESERVE_NODE_ID}"
  fi
}

backup_existing() {
  BACKUP_DIR="$(mktemp -d /tmp/nyxveil-install-backup.XXXXXX)"
  if [[ -f "${CONFIG_FILE}" ]]; then
    cp -a "${CONFIG_FILE}" "${BACKUP_DIR}/server.json"
  fi
  if [[ -f "${NODE_KEY}" ]]; then
    cp -a "${NODE_KEY}" "${BACKUP_DIR}/node.key"
  fi
  if [[ -f "${SERVICE_UNIT}" ]]; then
    cp -a "${SERVICE_UNIT}" "${BACKUP_DIR}/nyxveil-server.service"
  fi
  if [[ -f "${SYSCTL_FILE}" ]]; then
    cp -a "${SYSCTL_FILE}" "${BACKUP_DIR}/99-nyxveil.conf"
  fi
  if [[ -f "${NFT_FILE}" ]]; then
    cp -a "${NFT_FILE}" "${BACKUP_DIR}/nyxveil.nft"
  fi
  if [[ -x "${BIN_DIR}/nyxveil-server" ]]; then
    cp -a "${BIN_DIR}/nyxveil-server" "${BACKUP_DIR}/nyxveil-server" || true
  fi
  if [[ -x "${BIN_DIR}/nyxveilctl" ]]; then
    cp -a "${BIN_DIR}/nyxveilctl" "${BACKUP_DIR}/nyxveilctl" || true
  fi
}

rollback() {
  [[ "${COMMITTED}" -eq 0 ]] || return 0
  warn "rolling back incomplete install…"
  if [[ "${STARTED_SERVICE}" -eq 1 ]]; then
    systemctl stop nyxveil-server 2>/dev/null || true
    systemctl disable nyxveil-server 2>/dev/null || true
  fi
  if [[ "${INSTALLED_UNIT}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil-server.service" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil-server.service" "${SERVICE_UNIT}"
      systemctl daemon-reload || true
    else
      rm -f "${SERVICE_UNIT}"
      systemctl daemon-reload || true
    fi
  fi
  if [[ "${INSTALLED_SYSCTL}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/99-nyxveil.conf" ]]; then
      cp -a "${BACKUP_DIR}/99-nyxveil.conf" "${SYSCTL_FILE}"
    else
      rm -f "${SYSCTL_FILE}"
    fi
    sysctl --system >/dev/null 2>&1 || true
  fi
  if [[ "${INSTALLED_NFT}" -eq 1 ]]; then
    nft delete table inet nyxveil 2>/dev/null || true
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil.nft" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil.nft" "${NFT_FILE}"
      nft -f "${NFT_FILE}" 2>/dev/null || true
    else
      rm -f "${NFT_FILE}"
    fi
  fi
  if [[ "${INSTALLED_BINARIES}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil-server" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil-server" "${BIN_DIR}/nyxveil-server"
    else
      rm -f "${BIN_DIR}/nyxveil-server"
    fi
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveilctl" ]]; then
      cp -a "${BACKUP_DIR}/nyxveilctl" "${BIN_DIR}/nyxveilctl"
    else
      rm -f "${BIN_DIR}/nyxveilctl"
    fi
  fi
  if [[ "${WROTE_CONFIG}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/server.json" ]]; then
      cp -a "${BACKUP_DIR}/server.json" "${CONFIG_FILE}"
    else
      rm -f "${CONFIG_FILE}"
    fi
  fi
  # Never delete preserved node.key on repair failure if we had one.
  if [[ "${PRESERVE_HAD_KEY}" -eq 0 && -f "${NODE_KEY}" && ! -f "${BACKUP_DIR}/node.key" ]]; then
    rm -f "${NODE_KEY}"
  elif [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/node.key" ]]; then
    cp -a "${BACKUP_DIR}/node.key" "${NODE_KEY}"
  fi
  if [[ "${CREATED_USER}" -eq 1 ]]; then
    userdel nyxveil 2>/dev/null || true
  fi
  [[ -n "${BACKUP_DIR}" && -d "${BACKUP_DIR}" ]] && rm -rf "${BACKUP_DIR}"
  warn "rollback complete"
}

on_exit() {
  local code=$?
  if [[ "${COMMITTED}" -eq 0 && "${code}" -ne 0 ]]; then
    rollback || true
  fi
  # Scrub token from environment
  unset BOOTSTRAP_TOKEN
  exit "${code}"
}

ensure_user() {
  if id -u nyxveil >/dev/null 2>&1; then
    log "user nyxveil already exists"
    return 0
  fi
  useradd --system --home-dir "${STATE_DIR}" --shell /usr/sbin/nologin \
    --comment "Nyxveil VPN node" nyxveil
  CREATED_USER=1
  log "created system user nyxveil"
}

ensure_dirs() {
  install -d -m 0755 -o root -g root "${ETC_DIR}"
  install -d -m 0700 -o nyxveil -g nyxveil "${STATE_DIR}"
  install -d -m 0755 -o nyxveil -g nyxveil "${RUN_DIR}"
  install -d -m 0755 -o root -g root /etc/nftables.d
  install -d -m 0755 -o root -g root "${BIN_DIR}"
}

resolve_unit_source() {
  if [[ -f "${REPO_ROOT}/systemd/nyxveil-server.service" ]]; then
    echo "${REPO_ROOT}/systemd/nyxveil-server.service"
  elif [[ -f "${SCRIPT_DIR}/../systemd/nyxveil-server.service" ]]; then
    echo "${SCRIPT_DIR}/../systemd/nyxveil-server.service"
  else
    die "systemd unit template not found next to installer"
  fi
}

download_or_copy_binaries() {
  local arch tmp
  arch="$(detect_arch)"

  if [[ -n "${BINARY_DIR}" ]]; then
    [[ -d "${BINARY_DIR}" ]] || die "binary dir not found: ${BINARY_DIR}"
    [[ -f "${BINARY_DIR}/nyxveil-server" ]] || die "missing ${BINARY_DIR}/nyxveil-server"
    [[ -f "${BINARY_DIR}/nyxveilctl" ]] || die "missing ${BINARY_DIR}/nyxveilctl"
    install -m 0755 "${BINARY_DIR}/nyxveil-server" "${BIN_DIR}/nyxveil-server"
    install -m 0755 "${BINARY_DIR}/nyxveilctl" "${BIN_DIR}/nyxveilctl"
    INSTALLED_BINARIES=1
    log "installed binaries from ${BINARY_DIR}"
    return 0
  fi

  if [[ "${SKIP_DOWNLOAD}" -eq 1 ]]; then
    die "--skip-download requires --binary-dir"
  fi

  tmp="$(mktemp -d /tmp/nyxveil-dl.XXXXXX)"
  local base tag srv ctl
  tag="server-v${NYXVEIL_VERSION}"
  base="https://github.com/${GITHUB_REPO}/releases/download/${tag}"
  srv="${base}/nyxveil-server-linux-${arch}"
  ctl="${base}/nyxveilctl-linux-${arch}"
  log "downloading ${srv}"
  curl -fsSL -o "${tmp}/nyxveil-server" "${srv}"
  log "downloading ${ctl}"
  curl -fsSL -o "${tmp}/nyxveilctl" "${ctl}"
  # Optional checksums
  if curl -fsSL -o "${tmp}/SHA256SUMS" "${base}/SHA256SUMS" 2>/dev/null; then
    (
      cd "${tmp}"
      grep -E "nyxveil-(server|ctl)-linux-${arch}$" SHA256SUMS | sha256sum -c -
    ) || die "SHA256 verification failed"
  else
    warn "SHA256SUMS not available for ${tag}; skipping verify"
  fi
  install -m 0755 "${tmp}/nyxveil-server" "${BIN_DIR}/nyxveil-server"
  install -m 0755 "${tmp}/nyxveilctl" "${BIN_DIR}/nyxveilctl"
  rm -rf "${tmp}"
  INSTALLED_BINARIES=1
  log "installed binaries to ${BIN_DIR}"
}

install_sysctl() {
  cat > "${SYSCTL_FILE}" <<'EOF'
# Nyxveil VPN node — enable IPv4 forwarding for client NAT
net.ipv4.ip_forward = 1
EOF
  chmod 0644 "${SYSCTL_FILE}"
  sysctl --system >/dev/null 2>&1 || sysctl -p "${SYSCTL_FILE}" >/dev/null
  INSTALLED_SYSCTL=1
  log "wrote ${SYSCTL_FILE}"
}

install_nftables() {
  # Isolated table only — NEVER flush the global ruleset.
  cat > "${NFT_FILE}" <<EOF
# Managed by Nyxveil installer — table inet nyxveil only
table inet nyxveil {
  chain input {
    type filter hook input priority filter - 10; policy accept;
    tcp dport ${TLS_PORT} ct state new accept comment "nyxveil-tls"
    udp dport ${QUIC_PORT} ct state new accept comment "nyxveil-quic"
  }

  chain forward {
    type filter hook forward priority filter - 10; policy accept;
    iifname "nyxveil0" accept comment "nyxveil-fwd-in"
    oifname "nyxveil0" accept comment "nyxveil-fwd-out"
  }

  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr ${VPN_SUBNET} oifname != "nyxveil0" masquerade comment "nyxveil-masq"
  }
}
EOF
  chmod 0644 "${NFT_FILE}"
  # Replace only our table; do not touch other tables.
  nft delete table inet nyxveil 2>/dev/null || true
  nft -f "${NFT_FILE}"
  INSTALLED_NFT=1
  log "applied nftables table inet nyxveil (no ruleset flush)"
}

install_systemd_unit() {
  local src
  src="$(resolve_unit_source)"
  install -m 0644 "${src}" "${SERVICE_UNIT}"
  systemctl daemon-reload
  INSTALLED_UNIT=1
  log "installed ${SERVICE_UNIT}"
}

generate_node_id() {
  if [[ -n "${PRESERVE_NODE_ID}" ]]; then
    echo "${PRESERVE_NODE_ID}"
    return 0
  fi
  local host rand
  host="$(hostname -s 2>/dev/null || hostname || echo node)"
  host="$(echo "${host}" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-' | cut -c1-24)"
  rand="$(openssl rand -hex 4 2>/dev/null || head -c 4 /dev/urandom | xxd -p)"
  echo "nv-${host}-${rand}"
}

json_str() {
  # Escape a string for JSON double quotes (no python dependency).
  local s=$1
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  s=${s//$'\n'/\\n}
  s=${s//$'\r'/\\r}
  s=${s//$'\t'/\\t}
  printf '"%s"' "${s}"
}

write_server_json() {
  local node_id server_name
  node_id="$(generate_node_id)"
  server_name="${PUBLIC_HOST:-${DISPLAY_NAME}}"
  # Token is NEVER written to disk.
  cat > "${CONFIG_FILE}" <<EOF
{
  "control_plane_url": $(json_str "${CONTROL_PLANE}"),
  "node_id": $(json_str "${node_id}"),
  "location_id": $(json_str "${LOCATION_ID}"),
  "display_name": $(json_str "${DISPLAY_NAME}"),
  "config_version": 0,
  "server_name": $(json_str "${server_name}"),
  "public_host": $(json_str "${PUBLIC_HOST}"),
  "tls_listen": $(json_str ":${TLS_PORT}"),
  "quic_listen": $(json_str ":${QUIC_PORT}"),
  "vpn_subnet_cidr": $(json_str "${VPN_SUBNET}"),
  "heartbeat_seconds": 30,
  "tls_cert_file": $(json_str "${STATE_DIR}/tls.crt"),
  "tls_key_file": $(json_str "${STATE_DIR}/tls.key")
}
EOF
  chmod 0644 "${CONFIG_FILE}"
  chown root:root "${CONFIG_FILE}"
  WROTE_CONFIG=1
  log "wrote ${CONFIG_FILE} (no bootstrap token)"
}

generate_identity_and_register() {
  # LoadOrCreate node.key via --register; preserves existing key on repair.
  chown nyxveil:nyxveil "${STATE_DIR}"
  if [[ -f "${NODE_KEY}" ]]; then
    chown nyxveil:nyxveil "${NODE_KEY}"
    chmod 0600 "${NODE_KEY}"
  fi
  log "registering with Control Plane…"
  # Run as root briefly so key creation under STATE_DIR works; then fix ownership.
  # Token only on argv for this one-shot process.
  if ! "${BIN_DIR}/nyxveil-server" --config "${CONFIG_FILE}" --register "${BOOTSTRAP_TOKEN}"; then
    die "Control Plane registration failed"
  fi
  if [[ -f "${NODE_KEY}" ]]; then
    chown nyxveil:nyxveil "${NODE_KEY}"
    chmod 0600 "${NODE_KEY}"
  fi
  # Clear token ASAP
  BOOTSTRAP_TOKEN=""
  unset BOOTSTRAP_TOKEN
  log "registration complete; identity at ${NODE_KEY}"
}

start_and_test() {
  systemctl enable nyxveil-server
  systemctl restart nyxveil-server
  STARTED_SERVICE=1
  local i
  for i in $(seq 1 30); do
    if systemctl is-active --quiet nyxveil-server; then
      break
    fi
    sleep 1
  done
  systemctl is-active --quiet nyxveil-server || die "nyxveil-server failed to become active"
  # Self-test via control CLI / unix socket
  if ! "${BIN_DIR}/nyxveilctl" health >/dev/null 2>&1; then
    # Give control socket a moment
    sleep 2
    "${BIN_DIR}/nyxveilctl" health >/dev/null 2>&1 || die "self-test failed: nyxveilctl health"
  fi
  log "self-test OK"
}

install_serv_wrappers() {
  local wrap
  wrap="${REPO_ROOT}/scripts/serv_wrappers.sh"
  if [[ -x "${wrap}" ]] || [[ -f "${wrap}" ]]; then
    bash "${wrap}" install || warn "serv_wrappers install failed (non-fatal)"
  else
    # Minimal inline wrappers if packaging omitted scripts/
    for cmd in status health start stop restart logs; do
      cat > "/usr/local/bin/serv_${cmd}" <<EOF
#!/usr/bin/env bash
exec ${BIN_DIR}/nyxveilctl ${cmd} "\$@"
EOF
      chmod 0755 "/usr/local/bin/serv_${cmd}"
    done
  fi
}

print_success() {
  cat <<EOF

========================================================================
  Nyxveil node installed successfully (v${NYXVEIL_VERSION})
========================================================================

  Config:   ${CONFIG_FILE}
  State:    ${STATE_DIR}
  Service:  systemctl status nyxveil-server

  Quick commands:
    serv_status
    serv_health
    serv_start | serv_stop | serv_restart
    serv_logs -f

  Or: nyxveilctl status | health | logs

========================================================================
EOF
}

main() {
  parse_args "$@"
  require_root
  check_os
  check_systemd
  detect_arch >/dev/null
  check_tun
  check_resources
  trap on_exit EXIT

  gather_inputs
  detect_repair
  backup_existing
  ensure_packages
  ensure_user
  ensure_dirs
  download_or_copy_binaries
  install_sysctl
  install_nftables
  install_systemd_unit
  write_server_json
  generate_identity_and_register
  start_and_test
  install_serv_wrappers

  COMMITTED=1
  [[ -n "${BACKUP_DIR}" && -d "${BACKUP_DIR}" ]] && rm -rf "${BACKUP_DIR}"
  print_success
}

main "$@"
