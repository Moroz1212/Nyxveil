#!/usr/bin/env bash
# Automated clean-host installer gate for Ubuntu 24.04 + systemd + /dev/net/tun.
# Run on a disposable VPS as root. Does NOT target production.
set -euo pipefail

REPORT=/tmp/nyxveil-clean-host-gate-report.txt
: >"$REPORT"
pass=0
fail=0

record() {
  local name="$1" status="$2" detail="${3:-}"
  echo "${name}=${status} ${detail}" | tee -a "$REPORT"
  if [[ "$status" == "FAIL" ]]; then fail=$((fail + 1)); else pass=$((pass + 1)); fi
}

require_root() { [[ "$(id -u)" -eq 0 ]] || { echo "root required"; exit 2; }; }
require_ubuntu() {
  . /etc/os-release
  [[ "${ID:-}" == "ubuntu" && "${VERSION_ID:-}" == "24.04" ]] || record os FAIL "want ubuntu 24.04 got ${ID:-}-${VERSION_ID:-}"
  record os PASS "${ID}-${VERSION_ID}"
}
require_systemd() {
  [[ "$(ps -p 1 -o comm=)" == "systemd" ]] || record systemd FAIL
  record systemd PASS
}
require_tun() {
  [[ -c /dev/net/tun ]] || record tun FAIL
  record tun PASS
}

usage() {
  cat <<EOF
Usage: $0 --installer /path/to/install.sh --control-plane URL --location ID --bootstrap-token TOKEN --public-host FQDN [install.sh args...]
EOF
}

INSTALLER=""
CP=""
LOC=""
BOOT=""
HOST=""
EXTRA=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --installer) INSTALLER="$2"; shift 2 ;;
    --control-plane) CP="$2"; shift 2 ;;
    --location) LOC="$2"; shift 2 ;;
    --bootstrap-token) BOOT="$2"; shift 2 ;;
    --public-host) HOST="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) EXTRA+=("$1"); shift ;;
  esac
done

require_root
require_ubuntu
require_systemd
require_tun

[[ -n "$INSTALLER" && -x "$INSTALLER" ]] || { record installer FAIL "missing"; exit 1; }
[[ -n "$CP" && -n "$LOC" && -n "$BOOT" && -n "$HOST" ]] || { record args FAIL; exit 1; }

# Ensure clean-ish state: refuse if node.key already present unless NYXVEIL_GATE_ALLOW_REPAIR=1
if [[ -f /var/lib/nyxveil/node.key && "${NYXVEIL_GATE_ALLOW_REPAIR:-0}" != "1" ]]; then
  record clean_state FAIL "node.key exists"
  exit 1
fi
record clean_state PASS

timeout 900 "$INSTALLER" \
  --control-plane "$CP" \
  --location "$LOC" \
  --bootstrap-token "$BOOT" \
  --public-host "$HOST" \
  --noninteractive \
  "${EXTRA[@]}" || { record install FAIL "exit=$?"; exit 1; }
record install PASS

systemctl is-active --quiet nyxveil-server.service && record service PASS || record service FAIL
/usr/local/sbin/nyxveilctl version | tee -a "$REPORT" || true
/usr/local/sbin/nyxveilctl health && record health PASS || record health FAIL

if grep -R --fixed-strings -- "$BOOT" /etc/nyxveil /var/lib/nyxveil /etc/systemd/system 2>/dev/null; then
  record token_leak FAIL
else
  record token_leak PASS
fi

echo "pass=$pass fail=$fail" | tee -a "$REPORT"
[[ "$fail" -eq 0 ]]
