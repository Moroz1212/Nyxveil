#!/usr/bin/env bash
# Nyxveil VPN node installer — Ubuntu 24.04 / systemd / nftables.
# Self-contained: curl|bash works with ONLY this file (systemd units embedded).
# Transactional: EXIT trap rolls back until COMMIT.
#
# Release verification (fail-closed when downloading):
#   1. Download release-manifest-linux-${arch}.json from tag server-v${VERSION}
#   2. Build CanonicalManifestBytes matching Go updater.CanonicalManifestBytes
#      (JSON object without "signature", field order/omitempty as encoding/json)
#   3. Verify Ed25519 signature (PureEd25519) with openssl pkeyutl -rawin -verify
#      against embedded UpdatePublicKey (PUB_HEX below)
#   4. Download each asset URL; sha256sum -c; install
#   Missing/invalid manifest, signature, or sha → die (no WARN skip).
# Local --binary-dir / --skip-download skips remote verify.
set -euo pipefail

readonly NYXVEIL_VERSION="${NYXVEIL_VERSION:-1.0.0}"
readonly GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"
# Same Ed25519 public key as internal/updater.UpdatePublicKey
readonly PUB_HEX="f63d2c8001df3d7b2efdd171a16463260cb7190d61ef564419cc0836777d176f"
readonly DEFAULT_VPN_SUBNET="10.66.0.0/24"
readonly MIN_RAM_MB_WARN=700
readonly MIN_DISK_MB=200

# Paths (overridden under NYXVEIL_INSTALL_MOCK=1)
ETC_DIR="/etc/nyxveil"
STATE_DIR="/var/lib/nyxveil"
RUN_DIR="/run/nyxveil"
BIN_DIR="/usr/local/sbin"
LINK_DIR="/usr/local/bin"
SYSCTL_FILE="/etc/sysctl.d/99-nyxveil.conf"
SERVICE_UNIT="/etc/systemd/system/nyxveil-server.service"
FIREWALL_UNIT="/etc/systemd/system/nyxveil-firewall.service"
NFT_FILE="/etc/nftables.d/nyxveil.conf"
CONFIG_FILE=""
NODE_KEY=""
MOCK=0

SCRIPT_DIR=""
if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]:-}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi

COMMITTED=0
CREATED_USER=0
INSTALLED_BINARIES=0
INSTALLED_UNIT=0
INSTALLED_FIREWALL_UNIT=0
INSTALLED_SYSCTL=0
INSTALLED_NFT=0
WROTE_CONFIG=0
STARTED_SERVICE=0
BACKUP_DIR=""
PRESERVE_NODE_ID=""
PRESERVE_HAD_KEY=0
REPAIR_MODE=0

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
TEST_SELF_SIGNED=0
CONTROL_PLANE_CA_FILE=""
CONTROL_PLANE_SPKI_PIN=""
PINNED_CA_DEST=""

log()  { printf '[nyxveil] %s\n' "$*"; }
warn() { printf '[nyxveil] WARN: %s\n' "$*" >&2; }
die()  { printf '[nyxveil] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: install.sh [options]

  --control-plane URL          Control Plane base URL (https://...)
  --location ID                Location ID
  --name NAME                  Display name
  --bootstrap-token TOKEN      One-time registration token (never written to disk)
  --public-host HOST           Public hostname for endpoints (required in production)
  --public-ip IP               Public IPv4 (used if --public-host omitted)
  --tls-port PORT              TLS listen port (default 443)
  --quic-port PORT             QUIC listen port (default 443)
  --binary-dir DIR             Local directory with nyxveil-server + nyxveilctl
  --skip-download              Do not fetch release binaries (requires --binary-dir)
  --vpn-subnet CIDR            VPN client subnet (default 10.66.0.0/24)
  --control-plane-ca-file PATH Pin Control Plane CA (written as pinned_ca_file)
  --control-plane-spki-pin HEX Pin peer SPKI SHA-256 (control_plane_spki_pin)
  --test-self-signed           Allow test/self-signed TLS mode (--test-mode on register)
  --non-interactive            Fail instead of prompting
  -h, --help                   Show this help

Pinned production example (42mou.ru):
  sudo bash install.sh --control-plane https://42mou.ru:8443 \
    --control-plane-ca-file /path/to/cp-ca.pem \
    --location ... --name ... --public-host ...

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
      --control-plane-ca-file) CONTROL_PLANE_CA_FILE="${2:-}"; shift 2 ;;
      --control-plane-spki-pin) CONTROL_PLANE_SPKI_PIN="${2:-}"; shift 2 ;;
      --test-self-signed) TEST_SELF_SIGNED=1; shift ;;
      --non-interactive) NONINTERACTIVE=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "unknown argument: $1" ;;
    esac
  done
}

init_paths() {
  if [[ "${NYXVEIL_INSTALL_MOCK:-}" == "1" ]]; then
    MOCK=1
    local prefix
    prefix="${NYXVEIL_INSTALL_MOCK_ROOT:-$(mktemp -d /tmp/nyxveil-mock.XXXXXX)}"
    log "MOCK mode: root=${prefix}"
    LINK_DIR="${prefix}/usr/local/bin"
    ETC_DIR="${prefix}/etc/nyxveil"
    STATE_DIR="${prefix}/var/lib/nyxveil"
    RUN_DIR="${prefix}/run/nyxveil"
    BIN_DIR="${prefix}/usr/local/sbin"
    SYSCTL_FILE="${prefix}/etc/sysctl.d/99-nyxveil.conf"
    SERVICE_UNIT="${prefix}/etc/systemd/system/nyxveil-server.service"
    FIREWALL_UNIT="${prefix}/etc/systemd/system/nyxveil-firewall.service"
    NFT_FILE="${prefix}/etc/nftables.d/nyxveil.conf"
  fi
  CONFIG_FILE="${ETC_DIR}/server.json"
  NODE_KEY="${STATE_DIR}/node.key"
}

require_root() {
  [[ "${MOCK}" -eq 1 ]] && return 0
  [[ "$(id -u)" -eq 0 ]] || die "must run as root (sudo)"
}

check_os() {
  [[ "${MOCK}" -eq 1 ]] && return 0
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
  [[ "${MOCK}" -eq 1 ]] && return 0
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
  [[ "${MOCK}" -eq 1 ]] && return 0
  if [[ ! -e /dev/net/tun ]]; then
    die "/dev/net/tun missing — enable TUN/TAP (modprobe tun) before installing"
  fi
  if [[ ! -c /dev/net/tun ]]; then
    die "/dev/net/tun is not a character device"
  fi
}

check_resources() {
  [[ "${MOCK}" -eq 1 ]] && return 0
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
  [[ "${MOCK}" -eq 1 ]] && return 0
  local need=()
  local p
  for p in nftables iproute2 ca-certificates curl openssl jq; do
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

detect_repair() {
  PRESERVE_NODE_ID=""
  PRESERVE_HAD_KEY=0
  REPAIR_MODE=0
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
  if [[ -f "${NODE_KEY}" && -n "${PRESERVE_NODE_ID}" ]]; then
    REPAIR_MODE=1
    log "repair mode: node.key + node_id present — bootstrap token not required (PoP re-register)"
  fi
}

gather_inputs() {
  prompt_if_empty CONTROL_PLANE "Control Plane URL (https://...)"
  prompt_if_empty LOCATION_ID "Location ID"
  prompt_if_empty DISPLAY_NAME "Node display name"

  if [[ "${REPAIR_MODE}" -eq 0 ]]; then
    prompt_if_empty BOOTSTRAP_TOKEN "Bootstrap token" 1
    [[ -n "${BOOTSTRAP_TOKEN}" ]] || die "bootstrap token required"
  else
    log "skipping bootstrap prompt (repair / PoP)"
  fi

  [[ -n "${CONTROL_PLANE}" ]] || die "control plane URL required"
  [[ -n "${LOCATION_ID}" ]] || die "location ID required"
  [[ -n "${DISPLAY_NAME}" ]] || die "display name required"

  if [[ -z "${PUBLIC_HOST}" && -n "${PUBLIC_IP}" ]]; then
    PUBLIC_HOST="${PUBLIC_IP}"
  fi
  if [[ -z "${PUBLIC_HOST}" ]]; then
    if [[ "${TEST_SELF_SIGNED}" -eq 1 ]]; then
      warn "public_host empty with --test-self-signed"
    else
      if [[ "${NONINTERACTIVE}" -eq 0 ]]; then
        if [[ -r /dev/tty ]]; then
          read -r -p "Public host or IP (required): " PUBLIC_HOST < /dev/tty || true
        elif [[ -t 0 ]]; then
          read -r -p "Public host or IP (required): " PUBLIC_HOST || true
        fi
      fi
      [[ -n "${PUBLIC_HOST}" ]] || die "PUBLIC_HOST or PUBLIC_IP required in production (or pass --test-self-signed)"
    fi
  fi

  if [[ -n "${CONTROL_PLANE_CA_FILE}" ]]; then
    [[ -f "${CONTROL_PLANE_CA_FILE}" ]] || die "control-plane CA file not found: ${CONTROL_PLANE_CA_FILE}"
  fi
  if [[ -n "${CONTROL_PLANE_SPKI_PIN}" ]]; then
    case "${CONTROL_PLANE_SPKI_PIN}" in
      *[!0-9a-fA-F]*|'') die "invalid --control-plane-spki-pin (hex SHA-256)" ;;
    esac
    if [[ "${#CONTROL_PLANE_SPKI_PIN}" -ne 64 ]]; then
      die "invalid --control-plane-spki-pin length (want 64 hex chars)"
    fi
  fi

  case "${TLS_PORT}" in
    ''|*[!0-9]*) die "invalid --tls-port" ;;
  esac
  case "${QUIC_PORT}" in
    ''|*[!0-9]*) die "invalid --quic-port" ;;
  esac
}

# Precheck TLS to Control Plane before mutable install steps.
precheck_control_plane_tls() {
  [[ "${MOCK}" -eq 1 ]] && return 0
  local url hostport
  url="${CONTROL_PLANE%/}"
  [[ "${url}" == https://* ]] || die "control plane URL must be https://"

  log "prechecking TLS to Control Plane…"
  if [[ -n "${CONTROL_PLANE_CA_FILE}" ]]; then
    curl -fsS --connect-timeout 10 --max-time 30 \
      --cacert "${CONTROL_PLANE_CA_FILE}" \
      -o /dev/null -w '' "${url}/" 2>/dev/null \
      || curl -fsS --connect-timeout 10 --max-time 30 \
           --cacert "${CONTROL_PLANE_CA_FILE}" \
           -o /dev/null "${url}/health" 2>/dev/null \
      || curl -fsS --connect-timeout 10 --max-time 30 \
           --cacert "${CONTROL_PLANE_CA_FILE}" \
           -o /dev/null "${url}" \
      || die "TLS precheck failed (cacert) against ${url}"
    log "TLS precheck OK (pinned CA)"
    return 0
  fi

  if [[ -n "${CONTROL_PLANE_SPKI_PIN}" ]]; then
    hostport="${url#https://}"
    hostport="${hostport%%/*}"
    local host onlyhost tmp pin
    host="${hostport%%:*}"
    onlyhost="${host#[}"
    onlyhost="${onlyhost%]}"
    tmp="$(mktemp)"
    # SelfSignedPinned: fetch leaf without requiring system trust; pin is the trust anchor.
    # Match Go runtime: SPKI + hostname/SAN + NotBefore/NotAfter.
    if ! echo | openssl s_client -connect "${hostport}" -servername "${host}" 2>/dev/null \
         | openssl x509 -outform PEM > "${tmp}" 2>/dev/null; then
      rm -f "${tmp}"
      die "TLS precheck failed: could not fetch peer certificate from ${hostport}"
    fi
    pin="$(openssl x509 -in "${tmp}" -pubkey -noout 2>/dev/null \
      | openssl pkey -pubin -outform DER 2>/dev/null \
      | openssl dgst -sha256 -hex 2>/dev/null \
      | awk '{print $NF}')"
    [[ -n "${pin}" ]] || { rm -f "${tmp}"; die "TLS precheck failed: could not compute SPKI pin"; }
    if [[ "${pin,,}" != "${CONTROL_PLANE_SPKI_PIN,,}" ]]; then
      rm -f "${tmp}"
      die "TLS precheck SPKI pin mismatch (got ${pin})"
    fi
    if ! openssl x509 -in "${tmp}" -noout -checkhost "${onlyhost}" >/dev/null 2>&1; then
      rm -f "${tmp}"
      die "TLS precheck hostname mismatch for ${onlyhost}"
    fi
    if ! openssl x509 -in "${tmp}" -noout -checkend 0 >/dev/null 2>&1; then
      rm -f "${tmp}"
      die "TLS precheck failed: peer certificate expired"
    fi
    local not_before_raw not_before_epoch now_epoch
    now_epoch="$(date -u +%s)"
    not_before_raw="$(openssl x509 -in "${tmp}" -noout -startdate 2>/dev/null | sed 's/^notBefore=//')"
    not_before_epoch="$(date -u -d "${not_before_raw}" +%s 2>/dev/null || true)"
    if [[ -n "${not_before_epoch}" ]] && [[ "${now_epoch}" -lt "${not_before_epoch}" ]]; then
      rm -f "${tmp}"
      die "TLS precheck failed: peer certificate not yet valid"
    fi
    rm -f "${tmp}"
    log "TLS precheck OK (SelfSignedPinned SPKI)"
    return 0
  fi

  curl -fsS --connect-timeout 10 --max-time 30 -o /dev/null "${url}/" 2>/dev/null \
    || curl -fsS --connect-timeout 10 --max-time 30 -o /dev/null "${url}/health" 2>/dev/null \
    || curl -fsS --connect-timeout 10 --max-time 30 -o /dev/null "${url}" \
    || die "TLS precheck failed against ${url} (pass --control-plane-ca-file or --control-plane-spki-pin for pinned/self-signed)"
  log "TLS precheck OK"
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
  if [[ -f "${FIREWALL_UNIT}" ]]; then
    cp -a "${FIREWALL_UNIT}" "${BACKUP_DIR}/nyxveil-firewall.service"
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

sysctl_cmd() {
  [[ "${MOCK}" -eq 1 ]] && return 0
  command sysctl "$@"
}

systemctl_cmd() {
  [[ "${MOCK}" -eq 1 ]] && return 0
  command systemctl "$@"
}

nft_cmd() {
  [[ "${MOCK}" -eq 1 ]] && return 0
  command nft "$@"
}

rollback() {
  [[ "${COMMITTED}" -eq 0 ]] || return 0
  warn "rolling back incomplete install…"
  if [[ "${STARTED_SERVICE}" -eq 1 ]]; then
    systemctl_cmd stop nyxveil-server 2>/dev/null || true
    systemctl_cmd disable nyxveil-server 2>/dev/null || true
    systemctl_cmd stop nyxveil-firewall 2>/dev/null || true
    systemctl_cmd disable nyxveil-firewall 2>/dev/null || true
  fi
  if [[ "${INSTALLED_UNIT}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil-server.service" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil-server.service" "${SERVICE_UNIT}"
      systemctl_cmd daemon-reload || true
    else
      rm -f "${SERVICE_UNIT}"
      systemctl_cmd daemon-reload || true
    fi
  fi
  if [[ "${INSTALLED_FIREWALL_UNIT}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil-firewall.service" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil-firewall.service" "${FIREWALL_UNIT}"
      systemctl_cmd daemon-reload || true
    else
      rm -f "${FIREWALL_UNIT}"
      systemctl_cmd daemon-reload || true
    fi
  fi
  if [[ "${INSTALLED_SYSCTL}" -eq 1 ]]; then
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/99-nyxveil.conf" ]]; then
      cp -a "${BACKUP_DIR}/99-nyxveil.conf" "${SYSCTL_FILE}"
    else
      rm -f "${SYSCTL_FILE}"
    fi
    sysctl_cmd --system >/dev/null 2>&1 || true
  fi
  if [[ "${INSTALLED_NFT}" -eq 1 ]]; then
    nft_cmd delete table inet nyxveil 2>/dev/null || true
    if [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/nyxveil.nft" ]]; then
      cp -a "${BACKUP_DIR}/nyxveil.nft" "${NFT_FILE}"
      nft_cmd -f "${NFT_FILE}" 2>/dev/null || true
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
  if [[ "${PRESERVE_HAD_KEY}" -eq 0 && -f "${NODE_KEY}" && ! -f "${BACKUP_DIR}/node.key" ]]; then
    rm -f "${NODE_KEY}"
  elif [[ -n "${BACKUP_DIR}" && -f "${BACKUP_DIR}/node.key" ]]; then
    cp -a "${BACKUP_DIR}/node.key" "${NODE_KEY}"
  fi
  if [[ "${CREATED_USER}" -eq 1 ]]; then
    [[ "${MOCK}" -eq 1 ]] || userdel nyxveil 2>/dev/null || true
  fi
  [[ -n "${BACKUP_DIR}" && -d "${BACKUP_DIR}" ]] && rm -rf "${BACKUP_DIR}"
  warn "rollback complete"
}

on_exit() {
  local code=$?
  if [[ "${COMMITTED}" -eq 0 && "${code}" -ne 0 ]]; then
    rollback || true
  fi
  unset BOOTSTRAP_TOKEN
  exit "${code}"
}

ensure_user() {
  if [[ "${MOCK}" -eq 1 ]]; then
    log "MOCK: skip useradd"
    return 0
  fi
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
  if [[ "${MOCK}" -eq 1 ]]; then
    mkdir -p "${ETC_DIR}" "${STATE_DIR}" "${RUN_DIR}" "$(dirname "${NFT_FILE}")" \
      "${BIN_DIR}" "$(dirname "${SERVICE_UNIT}")" "$(dirname "${SYSCTL_FILE}")"
    chmod 0755 "${ETC_DIR}" "${RUN_DIR}" "${BIN_DIR}" 2>/dev/null || true
    chmod 0700 "${STATE_DIR}" 2>/dev/null || true
    return 0
  fi
  install -d -m 0755 -o root -g root "${ETC_DIR}"
  install -d -m 0700 -o nyxveil -g nyxveil "${STATE_DIR}"
  install -d -m 0755 -o nyxveil -g nyxveil "${RUN_DIR}"
  install -d -m 0755 -o root -g root "$(dirname "${NFT_FILE}")"
  install -d -m 0755 -o root -g root "${BIN_DIR}"
  install -d -m 0755 -o root -g root "$(dirname "${SERVICE_UNIT}")"
  install -d -m 0755 -o root -g root "$(dirname "${SYSCTL_FILE}")"
}

# --- Ed25519 manifest verify (matches updater.CanonicalManifestBytes) ---------

hex_to_bin() {
  local hex="$1"
  local i
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

# Write Ed25519 SubjectPublicKeyInfo PEM for PUB_HEX.
write_update_pubkey_pem() {
  local dest="$1"
  local der
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

# Canonical signed payload: same shape as Go updater.CanonicalManifestBytes.
# Must match json.Marshal output EXACTLY (no trailing LF). Command substitution
# strips jq's trailing newline; printf '%s' must not add one back.
canonical_manifest_bytes() {
  local manifest="$1"
  local canonical

  command -v jq >/dev/null 2>&1 ||
    die "jq required to verify release manifest"

  canonical="$(
    jq -c '
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
             {
               name: .name,
               sha256: .sha256,
               url: .url
             }
           ]
         }
         else {}
         end)
    ' "${manifest}"
  )"

  printf '%s' "${canonical}"
}

verify_manifest_signature() {
  local manifest="$1"

  # Fail-closed on missing signature before requiring jq/openssl (curl|bash minimal hosts).
  local sig_b64
  sig_b64="$(sed -n 's/.*"signature"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${manifest}" | head -n1 || true)"
  if [[ -z "${sig_b64}" || "${sig_b64}" == "null" ]]; then
    # Also try jq if present (signature may be multiline — still fail closed).
    if command -v jq >/dev/null 2>&1; then
      sig_b64="$(jq -r '.signature // empty' "${manifest}")"
    fi
  fi
  [[ -n "${sig_b64}" && "${sig_b64}" != "null" ]] || die "release manifest missing signature (fail-closed)"

  command -v jq >/dev/null 2>&1 || die "jq required to verify release manifest"
  command -v openssl >/dev/null 2>&1 || die "openssl required to verify release manifest"

  local version arch
  version="$(jq -r '.version // empty' "${manifest}")"
  arch="$(jq -r '.arch // empty' "${manifest}")"
  [[ -n "${version}" ]] || die "release manifest missing version"
  [[ -n "${arch}" ]] || die "release manifest missing arch"

  local assets_n
  assets_n="$(jq -r '(.assets // []) | length' "${manifest}")"
  local legacy_ok=0
  if [[ "$(jq -r '.sha256 // empty' "${manifest}")" != "" && "$(jq -r '.url // empty' "${manifest}")" != "" ]]; then
    legacy_ok=1
  fi
  if [[ "${assets_n}" -eq 0 && "${legacy_ok}" -eq 0 ]]; then
    die "release manifest missing assets (and no legacy url/sha256)"
  fi

  local msg_file sig_file pem
  msg_file="$(mktemp)"
  sig_file="$(mktemp)"
  pem="$(mktemp)"
  canonical_manifest_bytes "${manifest}" > "${msg_file}"
  if ! b64url_decode "${sig_b64}" > "${sig_file}"; then
    if ! printf '%s' "${sig_b64}" | base64 -d > "${sig_file}" 2>/dev/null; then
      rm -f "${msg_file}" "${sig_file}" "${pem}"
      die "release manifest signature encoding invalid"
    fi
  fi
  write_update_pubkey_pem "${pem}"
  # PureEd25519: openssl pkeyutl -rawin verifies raw message bytes (no pre-hash).
  if ! openssl pkeyutl -verify -pubin -inkey "${pem}" -rawin -in "${msg_file}" -sigfile "${sig_file}" >/dev/null 2>&1; then
    rm -f "${msg_file}" "${sig_file}" "${pem}"
    die "release manifest Ed25519 signature invalid (fail-closed)"
  fi
  rm -f "${msg_file}" "${sig_file}" "${pem}"
  log "manifest signature OK (Ed25519)"
}

http_get() {
  local url="$1"
  local dest="$2"
  if [[ "${MOCK}" -eq 1 ]]; then
    if [[ "${url}" == *release-manifest* ]]; then
      # Unsigned by default — exercises fail-closed unless test supplies a file.
      if [[ -n "${NYXVEIL_INSTALL_MOCK_MANIFEST:-}" && -f "${NYXVEIL_INSTALL_MOCK_MANIFEST}" ]]; then
        cp -a "${NYXVEIL_INSTALL_MOCK_MANIFEST}" "${dest}"
      else
        cat > "${dest}" <<EOF
{"version":"${NYXVEIL_VERSION}","arch":"linux/amd64","min_core":"1.0.0","min_protocol":1,"assets":[{"name":"nyxveil-server","sha256":"00","url":"https://example.invalid/s"},{"name":"nyxveilctl","sha256":"00","url":"https://example.invalid/c"}]}
EOF
      fi
      return 0
    fi
    printf 'mock-binary\n' > "${dest}"
    return 0
  fi
  curl -fsSL -o "${dest}" "${url}" || die "download failed: ${url}"
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
    log "installed binaries from ${BINARY_DIR} (no remote verify)"
    return 0
  fi

  if [[ "${SKIP_DOWNLOAD}" -eq 1 ]]; then
    die "--skip-download requires --binary-dir"
  fi

  # Fail-closed remote path: signed manifest required.
  # (jq/openssl required only after a signature field is present.)

  tmp="$(mktemp -d /tmp/nyxveil-dl.XXXXXX)"
  local tag base man
  tag="server-v${NYXVEIL_VERSION}"
  base="https://github.com/${GITHUB_REPO}/releases/download/${tag}"
  man="${base}/release-manifest-linux-${arch}.json"
  log "downloading manifest ${man}"
  http_get "${man}" "${tmp}/manifest.json"
  verify_manifest_signature "${tmp}/manifest.json"

  local want_arch="linux/${arch}"
  local got_arch
  got_arch="$(jq -r '.arch' "${tmp}/manifest.json")"
  [[ "${got_arch}" == "${want_arch}" ]] || die "manifest arch mismatch: have ${got_arch} want ${want_arch}"

  local n i name sha url dest
  n="$(jq -r '.assets | length' "${tmp}/manifest.json")"
  [[ "${n}" -gt 0 ]] || die "manifest has no assets"
  for ((i = 0; i < n; i++)); do
    name="$(jq -r ".assets[${i}].name" "${tmp}/manifest.json")"
    sha="$(jq -r ".assets[${i}].sha256" "${tmp}/manifest.json")"
    url="$(jq -r ".assets[${i}].url" "${tmp}/manifest.json")"
    [[ -n "${name}" && -n "${sha}" && -n "${url}" ]] || die "manifest asset[${i}] missing fields"
    [[ "${sha}" =~ ^[0-9a-fA-F]{64}$ ]] || die "manifest asset ${name}: invalid sha256"
    dest="${tmp}/asset-${i}"
    log "downloading ${name}"
    http_get "${url}" "${dest}"
    echo "${sha}  ${dest}" | sha256sum -c - >/dev/null || die "SHA256 mismatch for ${name}"
    case "${name}" in
      nyxveil-server|server)
        install -m 0755 "${dest}" "${BIN_DIR}/nyxveil-server"
        ;;
      nyxveilctl|ctl)
        install -m 0755 "${dest}" "${BIN_DIR}/nyxveilctl"
        ;;
      *)
        warn "ignoring unknown asset ${name}"
        ;;
    esac
  done
  [[ -x "${BIN_DIR}/nyxveil-server" ]] || die "nyxveil-server not installed from manifest"
  [[ -x "${BIN_DIR}/nyxveilctl" ]] || die "nyxveilctl not installed from manifest"
  rm -rf "${tmp}"
  INSTALLED_BINARIES=1
  log "installed binaries to ${BIN_DIR} (manifest verified)"
}

install_sysctl() {
  mkdir -p "$(dirname "${SYSCTL_FILE}")"
  cat > "${SYSCTL_FILE}" <<'EOF'
# Nyxveil VPN node — enable IPv4 forwarding for client NAT
net.ipv4.ip_forward = 1
EOF
  chmod 0644 "${SYSCTL_FILE}"
  sysctl_cmd --system >/dev/null 2>&1 || sysctl_cmd -p "${SYSCTL_FILE}" >/dev/null 2>&1 || true
  INSTALLED_SYSCTL=1
  log "wrote ${SYSCTL_FILE}"
}

install_nftables() {
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
  nft_cmd delete table inet nyxveil 2>/dev/null || true
  nft_cmd -f "${NFT_FILE}" || [[ "${MOCK}" -eq 1 ]]
  INSTALLED_NFT=1
  log "applied nftables table inet nyxveil (no ruleset flush)"
}

# Embedded units — no resolve_unit_source / sibling systemd/ required.
write_firewall_unit() {
  cat > "${FIREWALL_UNIT}" <<'EOF'
[Unit]
Description=Nyxveil nftables firewall (table inet nyxveil)
Documentation=https://github.com/Moroz1212/Nyxveil/tree/main/server/docs/FIREWALL.md
Before=nyxveil-server.service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/nftables.d/nyxveil.conf
ExecStop=/usr/sbin/nft delete table inet nyxveil

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${FIREWALL_UNIT}"
}

write_server_unit() {
  cat > "${SERVICE_UNIT}" <<'EOF'
[Unit]
Description=Nyxveil VPN Node
Documentation=https://github.com/Moroz1212/Nyxveil/tree/main/server/docs
After=network-online.target nyxveil-firewall.service
Wants=network-online.target nyxveil-firewall.service

[Service]
Type=simple
User=nyxveil
Group=nyxveil
ExecStart=/usr/local/sbin/nyxveil-server --config /etc/nyxveil/server.json
Restart=on-failure
RestartSec=3
TimeoutStopSec=20

AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=true

ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=false
DeviceAllow=/dev/net/tun rw
ReadWritePaths=/var/lib/nyxveil /run/nyxveil
RuntimeDirectory=nyxveil
RuntimeDirectoryMode=0755
StateDirectory=
UMask=0077

ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictSUIDSGID=true
RestrictRealtime=true
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK AF_PACKET
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${SERVICE_UNIT}"
}

install_systemd_units() {
  write_firewall_unit
  write_server_unit
  systemctl_cmd daemon-reload
  systemctl_cmd enable nyxveil-firewall.service
  # Apply firewall now and mark active (oneshot RemainAfterExit).
  systemctl_cmd restart nyxveil-firewall.service || systemctl_cmd start nyxveil-firewall.service || true
  INSTALLED_FIREWALL_UNIT=1
  INSTALLED_UNIT=1
  log "installed ${FIREWALL_UNIT} and ${SERVICE_UNIT}"
}

generate_node_id() {
  if [[ -n "${PRESERVE_NODE_ID}" ]]; then
    echo "${PRESERVE_NODE_ID}"
    return 0
  fi
  local host rand
  host="$(hostname -s 2>/dev/null || hostname || echo node)"
  host="$(echo "${host}" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-' | cut -c1-24)"
  # Prefer od/hexdump over openssl/xxd (may be absent on minimal hosts).
  if command -v od >/dev/null 2>&1; then
    rand="$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  elif command -v hexdump >/dev/null 2>&1; then
    rand="$(hexdump -n 4 -e '4/1 "%02x"' /dev/urandom)"
  else
    # Pure bash fallback via /dev/urandom bytes
    rand="$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  fi
  echo "nv-${host}-${rand}"
}

json_str() {
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

  PINNED_CA_DEST=""
  if [[ -n "${CONTROL_PLANE_CA_FILE}" ]]; then
    PINNED_CA_DEST="${ETC_DIR}/cp-ca.pem"
    install -m 0644 "${CONTROL_PLANE_CA_FILE}" "${PINNED_CA_DEST}"
  fi

  {
    printf '{\n'
    printf '  "control_plane_url": %s,\n' "$(json_str "${CONTROL_PLANE}")"
    printf '  "node_id": %s,\n' "$(json_str "${node_id}")"
    printf '  "location_id": %s,\n' "$(json_str "${LOCATION_ID}")"
    printf '  "display_name": %s,\n' "$(json_str "${DISPLAY_NAME}")"
    printf '  "config_version": 0,\n'
    printf '  "server_name": %s,\n' "$(json_str "${server_name}")"
    printf '  "public_host": %s,\n' "$(json_str "${PUBLIC_HOST}")"
    printf '  "tls_listen": %s,\n' "$(json_str ":${TLS_PORT}")"
    printf '  "quic_listen": %s,\n' "$(json_str ":${QUIC_PORT}")"
    printf '  "vpn_subnet_cidr": %s,\n' "$(json_str "${VPN_SUBNET}")"
    printf '  "heartbeat_seconds": 30,\n'
    printf '  "tls_cert_file": %s,\n' "$(json_str "${STATE_DIR}/tls.crt")"
    printf '  "tls_key_file": %s' "$(json_str "${STATE_DIR}/tls.key")"
    if [[ -n "${PINNED_CA_DEST}" ]]; then
      printf ',\n  "pinned_ca_file": %s' "$(json_str "${PINNED_CA_DEST}")"
    fi
    if [[ -n "${CONTROL_PLANE_SPKI_PIN}" ]]; then
      printf ',\n  "control_plane_spki_pin": %s' "$(json_str "${CONTROL_PLANE_SPKI_PIN}")"
    fi
    printf '\n}\n'
  } > "${CONFIG_FILE}"
  chmod 0644 "${CONFIG_FILE}"
  chown root:root "${CONFIG_FILE}" 2>/dev/null || true
  WROTE_CONFIG=1
  log "wrote ${CONFIG_FILE} (no bootstrap token)"
}

generate_identity_and_register() {
  if [[ "${MOCK}" -eq 1 ]]; then
    # Touch a placeholder key so repair-path tests can see state layout.
    if [[ ! -f "${NODE_KEY}" ]]; then
      printf 'mock-node-key\n' > "${NODE_KEY}"
      chmod 0600 "${NODE_KEY}"
    fi
    # Simulate TLS material ownership layout expected after real register.
    printf 'mock-tls-cert\n' > "${STATE_DIR}/tls.crt"
    printf 'mock-tls-key\n' > "${STATE_DIR}/tls.key"
    chmod 0644 "${STATE_DIR}/tls.crt"
    chmod 0600 "${STATE_DIR}/tls.key"
    fix_state_ownership
    log "MOCK: skip Control Plane registration"
    BOOTSTRAP_TOKEN=""
    unset BOOTSTRAP_TOKEN
    return 0
  fi

  # State dir must be owned by service user before register creates keys/TLS.
  install -d -m 0700 -o nyxveil -g nyxveil "${STATE_DIR}"
  if [[ -f "${NODE_KEY}" ]]; then
    chown nyxveil:nyxveil "${NODE_KEY}"
    chmod 0600 "${NODE_KEY}"
  fi
  # server.json + pinned CA stay root-owned but world/group-readable for nyxveil.
  chmod 0644 "${CONFIG_FILE}" 2>/dev/null || true
  if [[ -n "${PINNED_CA_DEST}" && -f "${PINNED_CA_DEST}" ]]; then
    chmod 0644 "${PINNED_CA_DEST}"
  fi

  log "registering with Control Plane as user nyxveil…"
  local reg_flags=(--config "${CONFIG_FILE}" --register-stdin)
  if [[ "${TEST_SELF_SIGNED}" -eq 1 ]]; then
    reg_flags+=(--test-mode)
  fi
  if [[ "${REPAIR_MODE}" -eq 1 ]]; then
    # Empty bootstrap: Register uses PoP NodeToken when node.key already exists.
    if ! printf '\n' | run_as_nyxveil "${BIN_DIR}/nyxveil-server" "${reg_flags[@]}"; then
      die "Control Plane repair re-registration (PoP) failed"
    fi
  else
    if ! printf '%s\n' "${BOOTSTRAP_TOKEN}" | run_as_nyxveil "${BIN_DIR}/nyxveil-server" "${reg_flags[@]}"; then
      die "Control Plane registration failed"
    fi
  fi
  fix_state_ownership
  BOOTSTRAP_TOKEN=""
  unset BOOTSTRAP_TOKEN
  log "registration complete; identity at ${NODE_KEY}"
}

# run_as_nyxveil executes a command as the runtime service user (no root TLS keys).
run_as_nyxveil() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u nyxveil -- "$@"
    return $?
  fi
  if command -v setpriv >/dev/null 2>&1; then
    setpriv --reuid=nyxveil --regid=nyxveil --clear-groups -- "$@"
    return $?
  fi
  # Fallback: su with preserved argv via env.
  local cmd
  cmd="$(printf '%q ' "$@")"
  su -s /bin/bash nyxveil -c "${cmd}"
}

# fix_state_ownership enforces service-user ownership on all private state.
fix_state_ownership() {
  chmod 0700 "${STATE_DIR}" 2>/dev/null || true
  [[ -f "${NODE_KEY}" ]] && chmod 0600 "${NODE_KEY}"
  [[ -f "${STATE_DIR}/tls.key" ]] && chmod 0600 "${STATE_DIR}/tls.key"
  [[ -f "${STATE_DIR}/tls.crt" ]] && chmod 0644 "${STATE_DIR}/tls.crt"
  [[ -f "${STATE_DIR}/applied-config.json" ]] && chmod 0600 "${STATE_DIR}/applied-config.json"
  [[ -f "${STATE_DIR}/ech-private.key" ]] && chmod 0600 "${STATE_DIR}/ech-private.key"
  [[ -f "${STATE_DIR}/ticket-keys.json" ]] && chmod 0600 "${STATE_DIR}/ticket-keys.json"
  [[ "${MOCK}" -eq 1 ]] && return 0
  chown -R nyxveil:nyxveil "${STATE_DIR}"
  if [[ -f "${NODE_KEY}" ]]; then
    chown nyxveil:nyxveil "${NODE_KEY}"
  fi
  if [[ -f "${STATE_DIR}/tls.key" ]]; then
    chown nyxveil:nyxveil "${STATE_DIR}/tls.key"
  fi
  if [[ -f "${STATE_DIR}/tls.crt" ]]; then
    chown nyxveil:nyxveil "${STATE_DIR}/tls.crt"
  fi
  if [[ -f "${STATE_DIR}/applied-config.json" ]]; then
    chown nyxveil:nyxveil "${STATE_DIR}/applied-config.json"
  fi
  if [[ -f "${STATE_DIR}/ech-private.key" ]]; then
    chown nyxveil:nyxveil "${STATE_DIR}/ech-private.key"
  fi
  if [[ -f "${STATE_DIR}/ticket-keys.json" ]]; then
    chown nyxveil:nyxveil "${STATE_DIR}/ticket-keys.json"
  fi
}

start_and_test() {
  if [[ "${MOCK}" -eq 1 ]]; then
    log "MOCK: skip systemctl start / health gate"
    STARTED_SERVICE=1
    return 0
  fi
  systemctl_cmd enable nyxveil-firewall.service
  systemctl_cmd enable nyxveil-server
  systemctl_cmd restart nyxveil-firewall.service || true
  systemctl_cmd restart nyxveil-server
  STARTED_SERVICE=1
  local i
  for i in $(seq 1 30); do
    if systemctl_cmd is-active --quiet nyxveil-server; then
      break
    fi
    sleep 1
  done
  systemctl_cmd is-active --quiet nyxveil-server || die "nyxveil-server failed to become active"

  local ok=0
  for i in $(seq 1 60); do
    if "${BIN_DIR}/nyxveilctl" health >/dev/null 2>&1; then
      ok=1
      break
    fi
    sleep 1
  done
  [[ "${ok}" -eq 1 ]] || die "self-test failed: nyxveilctl health unhealthy after 60s"
  log "self-test OK"
}

install_serv_wrappers() {
  local wrap="" cmds cmd
  if [[ -n "${SCRIPT_DIR}" ]]; then
    wrap="${SCRIPT_DIR}/../scripts/serv_wrappers.sh"
  fi
  cmds=(status health start stop restart logs version config update uninstall)
  mkdir -p "${LINK_DIR}"

  # Prefer repo script when present (offline tarball). curl|bash uses embedded list.
  if [[ "${MOCK}" -eq 0 && -n "${wrap}" && -f "${wrap}" ]]; then
    if NYXVEIL_BIN_DIR="${BIN_DIR}" NYXVEIL_LINK_DIR="${LINK_DIR}" bash "${wrap}" install; then
      return 0
    fi
    warn "serv_wrappers install failed; writing embedded wrappers"
  fi

  for cmd in "${cmds[@]}"; do
    cat > "${LINK_DIR}/serv_${cmd}" <<EOF
#!/usr/bin/env bash
exec ${BIN_DIR}/nyxveilctl ${cmd} "\$@"
EOF
    chmod 0755 "${LINK_DIR}/serv_${cmd}"
  done
  log "installed serv_* wrappers in ${LINK_DIR}"
}

print_success() {
  cat <<EOF

========================================================================
  Nyxveil node installed successfully (v${NYXVEIL_VERSION})
========================================================================

  Config:   ${CONFIG_FILE}  (static, root-owned, daemon read-only)
  Applied:  ${STATE_DIR}/applied-config.json  (dynamic CP state)
  State:    ${STATE_DIR}
  Service:  systemctl status nyxveil-server
  Firewall: systemctl status nyxveil-firewall

  Quick commands:
    serv_status
    serv_health
    serv_restart
    serv_update
    serv_logs
    serv_version

  Or: nyxveilctl status | health | logs | update | version

========================================================================
EOF
}

main() {
  # CI / interop helpers (no root, no install side effects).
  if [[ "${1:-}" == "--verify-manifest" ]]; then
    [[ -n "${2:-}" && -f "${2}" ]] || die "usage: install.sh --verify-manifest PATH"
    verify_manifest_signature "$2"
    exit 0
  fi
  if [[ "${1:-}" == "--dump-canonical" ]]; then
    [[ -n "${2:-}" && -f "${2}" ]] || die "usage: install.sh --dump-canonical PATH"
    canonical_manifest_bytes "$2"
    exit 0
  fi

  parse_args "$@"
  init_paths
  require_root
  check_os
  check_systemd
  detect_arch >/dev/null
  check_tun
  check_resources
  trap on_exit EXIT

  detect_repair
  gather_inputs
  precheck_control_plane_tls

  backup_existing
  ensure_packages
  ensure_user
  ensure_dirs
  download_or_copy_binaries
  install_sysctl
  install_nftables
  install_systemd_units
  write_server_json
  generate_identity_and_register
  start_and_test
  install_serv_wrappers

  COMMITTED=1
  [[ -n "${BACKUP_DIR}" && -d "${BACKUP_DIR}" ]] && rm -rf "${BACKUP_DIR}"
  print_success
}

main "$@"
