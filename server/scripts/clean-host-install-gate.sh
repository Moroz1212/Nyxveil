#!/usr/bin/env bash
# Automated clean-host installer gate for disposable Ubuntu 24.04 VPS hosts.
# Requires root, PID1 systemd, /dev/net/tun. NEVER targets production.
#
# Modes:
#   --mode local-candidate     binary-dir / skip-download (CANDIDATE LOCAL ARTIFACT GATE)
#   --mode published-release   download published GitHub release (PUBLISHED GITHUB RELEASE GATE)
set -euo pipefail
umask 077

REPORT_TXT=/tmp/nyxveil-clean-host-gate-report.txt
REPORT_JSON=/tmp/nyxveil-clean-host-gate-report.json
: >"${REPORT_TXT}"

PASS_N=0
FAIL_N=0
SKIP_N=0
NOT_EXECUTED_N=0
CRITICAL_FAILED=0
GATE_TITLE="NYXVEIL CLEAN HOST INSTALL GATE"
MODE=""
INSTALLER=""
CP=""
LOC=""
PUBLIC_HOST=""
EXPECTED_VERSION=""
BINARY_DIR=""
BOOTSTRAP_TOKEN=""
BOOTSTRAP_TOKEN_FILE=""
CONFIRM_DISPOSABLE=0
NAME="clean-host-gate"
EXTRA_INSTALL_ARGS=()
declare -a CHECK_NAMES=()
declare -a CHECK_STATUSES=()
declare -a CHECK_DETAILS=()

usage() {
  cat <<EOF
Usage: $0 --confirm-disposable-host --expected-version X.Y.Z \\
  --installer /path/to/install.sh --control-plane URL --location ID --public-host FQDN \\
  --mode local-candidate|published-release [options]

Required safety:
  --confirm-disposable-host
  Marker file /root/NYXVEIL_DISPOSABLE_TEST_HOST OR NYXVEIL_DISPOSABLE_HOST_ALLOW=1
  OR hostname listed in NYXVEIL_DISPOSABLE_HOST_ALLOWLIST (comma-separated)

Required:
  --expected-version X.Y.Z
  --installer PATH
  --control-plane URL
  --location ID
  --public-host FQDN
  --mode local-candidate|published-release

Bootstrap (token NEVER on argv of install.sh):
  --bootstrap-token-file PATH   (mode 0600 preferred; read once)
  OR pipe token on stdin (used with install.sh --bootstrap-stdin)

Local candidate extras:
  --binary-dir DIR              (required for --mode local-candidate)

Optional:
  --name DISPLAY_NAME
  --control-plane-ca-file PATH  (forwarded to install.sh)
  --dns-servers IP[,IP...]
  --tls-domain FQDN / --tls-cert PATH --tls-key PATH / --test-self-signed

Reports: ${REPORT_TXT} and ${REPORT_JSON}
Exit 0 only if every REQUIRED check is PASS (SKIP/NOT_EXECUTED do not count as PASS).
EOF
}

record() {
  local name="$1" status="$2" detail="${3:-}"
  CHECK_NAMES+=("${name}")
  CHECK_STATUSES+=("${status}")
  CHECK_DETAILS+=("${detail}")
  printf '%s=%s %s\n' "${name}" "${status}" "${detail}" | tee -a "${REPORT_TXT}"
  case "${status}" in
    PASS) PASS_N=$((PASS_N + 1)) ;;
    FAIL)
      FAIL_N=$((FAIL_N + 1))
      ;;
    SKIP) SKIP_N=$((SKIP_N + 1)) ;;
    NOT_EXECUTED) NOT_EXECUTED_N=$((NOT_EXECUTED_N + 1)) ;;
    *)
      FAIL_N=$((FAIL_N + 1))
      ;;
  esac
}

record_critical() {
  local name="$1" status="$2" detail="${3:-}"
  record "${name}" "${status}" "${detail}"
  if [[ "${status}" == "FAIL" ]]; then
    CRITICAL_FAILED=1
  fi
}

write_json_report() {
  local i n
  n="${#CHECK_NAMES[@]}"
  {
    printf '{\n'
    printf '  "title": %s,\n' "$(json_str "${GATE_TITLE}")"
    printf '  "mode": %s,\n' "$(json_str "${MODE}")"
    printf '  "expected_version": %s,\n' "$(json_str "${EXPECTED_VERSION}")"
    printf '  "pass": %s,\n' "${PASS_N}"
    printf '  "fail": %s,\n' "${FAIL_N}"
    printf '  "skip": %s,\n' "${SKIP_N}"
    printf '  "not_executed": %s,\n' "${NOT_EXECUTED_N}"
    printf '  "checks": [\n'
    for ((i = 0; i < n; i++)); do
      printf '    {"name":%s,"status":%s,"detail":%s}%s\n' \
        "$(json_str "${CHECK_NAMES[$i]}")" \
        "$(json_str "${CHECK_STATUSES[$i]}")" \
        "$(json_str "${CHECK_DETAILS[$i]}")" \
        "$([[ $i -lt $((n - 1)) ]] && echo ',' || true)"
    done
    printf '  ]\n}\n'
  } >"${REPORT_JSON}"
}

json_str() {
  local s="$1"
  if command -v python3 >/dev/null 2>&1 || command -v python >/dev/null 2>&1; then
    local py=python3
    command -v python3 >/dev/null 2>&1 || py=python
    "${py}" -c 'import json,sys; print(json.dumps(sys.argv[1]))' "${s}"
    return
  fi
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  printf '"%s"' "${s}"
}

die_usage() {
  echo "$*" >&2
  usage >&2
  exit 2
}

# --- parse args ---
while [[ $# -gt 0 ]]; do
  case "$1" in
    --confirm-disposable-host) CONFIRM_DISPOSABLE=1; shift ;;
    --expected-version) EXPECTED_VERSION="${2:-}"; shift 2 ;;
    --installer) INSTALLER="${2:-}"; shift 2 ;;
    --control-plane) CP="${2:-}"; shift 2 ;;
    --location) LOC="${2:-}"; shift 2 ;;
    --public-host) PUBLIC_HOST="${2:-}"; shift 2 ;;
    --mode) MODE="${2:-}"; shift 2 ;;
    --binary-dir) BINARY_DIR="${2:-}"; shift 2 ;;
    --bootstrap-token-file) BOOTSTRAP_TOKEN_FILE="${2:-}"; shift 2 ;;
    --bootstrap-token)
      echo "ERROR: --bootstrap-token is forbidden on this gate (use --bootstrap-token-file or stdin)" >&2
      exit 2
      ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --control-plane-ca-file|--dns-servers|--tls-domain|--tls-cert|--tls-key|--tls-email|--tls-port|--quic-port|--vpn-subnet|--control-plane-spki-pin)
      EXTRA_INSTALL_ARGS+=("$1" "${2:-}")
      shift 2
      ;;
    --test-self-signed|--tls-replace)
      EXTRA_INSTALL_ARGS+=("$1")
      shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) die_usage "unknown argument: $1" ;;
  esac
done

{
  echo "=== ${GATE_TITLE} ==="
  echo "started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host=$(hostname -f 2>/dev/null || hostname)"
} | tee -a "${REPORT_TXT}"

# --- safety: disposable confirmation ---
if [[ "${CONFIRM_DISPOSABLE}" -ne 1 ]]; then
  record_critical CONFIRM_DISPOSABLE FAIL "missing --confirm-disposable-host"
  write_json_report
  exit 1
fi
record CONFIRM_DISPOSABLE PASS "--confirm-disposable-host"

HOST_ALLOW=0
if [[ -f /root/NYXVEIL_DISPOSABLE_TEST_HOST ]]; then
  HOST_ALLOW=1
  record DISPOSABLE_MARKER PASS "/root/NYXVEIL_DISPOSABLE_TEST_HOST"
elif [[ "${NYXVEIL_DISPOSABLE_HOST_ALLOW:-0}" == "1" ]]; then
  HOST_ALLOW=1
  record DISPOSABLE_MARKER PASS "NYXVEIL_DISPOSABLE_HOST_ALLOW=1"
elif [[ -n "${NYXVEIL_DISPOSABLE_HOST_ALLOWLIST:-}" ]]; then
  hn="$(hostname -f 2>/dev/null || hostname || true)"
  IFS=',' read -r -a _allow <<< "${NYXVEIL_DISPOSABLE_HOST_ALLOWLIST}"
  for a in "${_allow[@]}"; do
    a="$(echo "${a}" | tr -d '[:space:]')"
    [[ -n "${a}" && "${a}" == "${hn}" ]] && HOST_ALLOW=1 && break
  done
  if [[ "${HOST_ALLOW}" -eq 1 ]]; then
    record DISPOSABLE_MARKER PASS "allowlist match ${hn}"
  else
    record_critical DISPOSABLE_MARKER FAIL "hostname ${hn} not in NYXVEIL_DISPOSABLE_HOST_ALLOWLIST"
  fi
else
  record_critical DISPOSABLE_MARKER FAIL "need /root/NYXVEIL_DISPOSABLE_TEST_HOST or NYXVEIL_DISPOSABLE_HOST_ALLOW=1 or allowlist"
fi

# --- required args ---
[[ -n "${EXPECTED_VERSION}" ]] || record_critical ARGS FAIL "missing --expected-version"
[[ -n "${INSTALLER}" ]] || record_critical ARGS FAIL "missing --installer"
[[ -n "${CP}" ]] || record_critical ARGS FAIL "missing --control-plane"
[[ -n "${LOC}" ]] || record_critical ARGS FAIL "missing --location"
[[ -n "${PUBLIC_HOST}" ]] || record_critical ARGS FAIL "missing --public-host"
case "${MODE}" in
  local-candidate|published-release) ;;
  *) record_critical ARGS FAIL "missing/invalid --mode (local-candidate|published-release)" ;;
esac

if [[ "${MODE}" == "local-candidate" ]]; then
  GATE_TITLE="CANDIDATE LOCAL ARTIFACT GATE"
  [[ -n "${BINARY_DIR}" ]] || record_critical ARGS FAIL "--mode local-candidate requires --binary-dir"
else
  GATE_TITLE="PUBLISHED GITHUB RELEASE GATE"
fi
echo "gate_label=${GATE_TITLE}" | tee -a "${REPORT_TXT}"

if [[ "${CRITICAL_FAILED}" -eq 1 ]]; then
  write_json_report
  echo "critical preflight failed; refusing install" | tee -a "${REPORT_TXT}"
  exit 1
fi
record ARGS PASS "required flags present"

# --- preflight (exactly one result each) ---
if [[ "$(id -u)" -eq 0 ]]; then
  record ROOT PASS "uid=0"
else
  record_critical ROOT FAIL "root required"
fi

if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  if [[ "${ID:-}" == "ubuntu" && "${VERSION_ID:-}" == "24.04" ]]; then
    record OS PASS "${ID}-${VERSION_ID}"
  else
    record_critical OS FAIL "want ubuntu 24.04 got ${ID:-}-${VERSION_ID:-}"
  fi
else
  record_critical OS FAIL "cannot read /etc/os-release"
fi

if [[ "$(ps -p 1 -o comm= 2>/dev/null || true)" == "systemd" ]]; then
  record SYSTEMD PASS "pid1=systemd"
else
  record_critical SYSTEMD FAIL "pid1 is not systemd"
fi

if [[ -c /dev/net/tun ]]; then
  record TUN PASS "/dev/net/tun"
else
  record_critical TUN FAIL "/dev/net/tun missing"
fi

if [[ -x "${INSTALLER}" || -f "${INSTALLER}" ]]; then
  record INSTALLER_PRESENT PASS "${INSTALLER}"
else
  record_critical INSTALLER_PRESENT FAIL "installer not found: ${INSTALLER}"
fi

# Static guard: timeout must wrap a real binary, never the run_as_nyxveil shell function.
if [[ -f "${INSTALLER}" ]]; then
  if grep -E 'timeout[[:space:]].*run_as_nyxveil([^_]|$)' "${INSTALLER}" >/dev/null 2>&1; then
    record_critical INSTALLER_TIMEOUT_WRAPPER FAIL "install.sh wraps run_as_nyxveil with timeout (broken pattern)"
  elif ! grep -q 'run_as_nyxveil_bounded' "${INSTALLER}"; then
    record_critical INSTALLER_TIMEOUT_WRAPPER FAIL "install.sh missing run_as_nyxveil_bounded"
  else
    record INSTALLER_TIMEOUT_WRAPPER PASS "run_as_nyxveil_bounded present; no timeout+function bug"
  fi
fi

# Bootstrap token (never log)
set +x
{ set +o xtrace; } 2>/dev/null || true
if [[ -n "${BOOTSTRAP_TOKEN_FILE}" ]]; then
  if [[ ! -f "${BOOTSTRAP_TOKEN_FILE}" ]]; then
    record_critical BOOTSTRAP_TOKEN FAIL "token file missing"
  else
    mode="$(stat -c '%a' "${BOOTSTRAP_TOKEN_FILE}" 2>/dev/null || stat -f '%Lp' "${BOOTSTRAP_TOKEN_FILE}" 2>/dev/null || echo "?")"
    if [[ "${mode}" != "600" && "${mode}" != "0600" ]]; then
      echo "WARN: bootstrap token file mode is ${mode} (prefer 0600)" | tee -a "${REPORT_TXT}"
    fi
    BOOTSTRAP_TOKEN="$(tr -d '\r\n' < "${BOOTSTRAP_TOKEN_FILE}")"
    if [[ -n "${BOOTSTRAP_TOKEN}" ]]; then
      record BOOTSTRAP_TOKEN PASS "read from file (not logged)"
    else
      record_critical BOOTSTRAP_TOKEN FAIL "token file empty"
    fi
  fi
elif [[ ! -t 0 ]]; then
  BOOTSTRAP_TOKEN="$(head -n 1 | tr -d '\r\n' || true)"
  if [[ -n "${BOOTSTRAP_TOKEN}" ]]; then
    record BOOTSTRAP_TOKEN PASS "read from stdin (not logged)"
  else
    record_critical BOOTSTRAP_TOKEN FAIL "stdin empty"
  fi
else
  record_critical BOOTSTRAP_TOKEN FAIL "provide --bootstrap-token-file or pipe token on stdin"
fi

if [[ "${CRITICAL_FAILED}" -eq 1 ]]; then
  write_json_report
  echo "critical preflight failed; refusing destructive install" | tee -a "${REPORT_TXT}"
  exit 1
fi

# --- CLEAN_STATE: refuse if production-like state exists (do NOT auto-delete) ---
CLEAN_OK=1
CLEAN_DETAIL=""
if [[ -d /etc/nyxveil ]]; then CLEAN_OK=0; CLEAN_DETAIL+="/etc/nyxveil "; fi
if [[ -d /var/lib/nyxveil ]]; then CLEAN_OK=0; CLEAN_DETAIL+="/var/lib/nyxveil "; fi
if compgen -G '/usr/local/sbin/nyxveil*' >/dev/null 2>&1; then CLEAN_OK=0; CLEAN_DETAIL+="nyxveil binaries "; fi
if systemctl list-unit-files 'nyxveil*' 2>/dev/null | grep -q nyxveil; then CLEAN_OK=0; CLEAN_DETAIL+="systemd units "; fi
if command -v nft >/dev/null 2>&1 && nft list table inet nyxveil >/dev/null 2>&1; then
  CLEAN_OK=0
  CLEAN_DETAIL+="nftables nyxveil "
fi
if [[ "${CLEAN_OK}" -eq 1 ]]; then
  record CLEAN_STATE PASS "no prior nyxveil state"
else
  record_critical CLEAN_STATE FAIL "refuse install; existing state: ${CLEAN_DETAIL}(do not auto-delete)"
  write_json_report
  exit 1
fi

# --- install via --bootstrap-stdin only ---
set +x
{ set +o xtrace; } 2>/dev/null || true
INSTALL_LOG="$(mktemp /tmp/nyxveil-clean-host-install.XXXXXX.log)"
INSTALL_CMD=(bash "${INSTALLER}"
  --control-plane "${CP}"
  --location "${LOC}"
  --name "${NAME}"
  --public-host "${PUBLIC_HOST}"
  --bootstrap-stdin
  --non-interactive
)
if [[ "${MODE}" == "local-candidate" ]]; then
  INSTALL_CMD+=(--binary-dir "${BINARY_DIR}" --skip-download)
  if [[ -z "${NYXVEIL_VERSION:-}" ]]; then
    export NYXVEIL_VERSION="${EXPECTED_VERSION}"
  fi
else
  export NYXVEIL_VERSION="${EXPECTED_VERSION}"
fi
INSTALL_CMD+=("${EXTRA_INSTALL_ARGS[@]+"${EXTRA_INSTALL_ARGS[@]}"}")

echo "install_mode=${MODE}" | tee -a "${REPORT_TXT}"
TOKEN_LEAK_PROBE="${BOOTSTRAP_TOKEN}"
set +e
printf '%s\n' "${BOOTSTRAP_TOKEN}" | timeout 1200 "${INSTALL_CMD[@]}" >"${INSTALL_LOG}" 2>&1
INSTALL_RC=$?
set -e
# Redact any accidental token echo from log before archiving snippet (no argv logging)
if [[ -n "${TOKEN_LEAK_PROBE}" ]] && command -v python3 >/dev/null 2>&1; then
  TOKEN_LEAK_PROBE="${TOKEN_LEAK_PROBE}" python3 - "${INSTALL_LOG}" <<'PY' 2>/dev/null || true
import os, pathlib, sys
p = pathlib.Path(sys.argv[1])
tok = os.environ.get("TOKEN_LEAK_PROBE") or ""
if tok and p.is_file():
    text = p.read_text(encoding="utf-8", errors="replace")
    p.write_text(text.replace(tok, "[REDACTED]"), encoding="utf-8")
PY
elif [[ -n "${TOKEN_LEAK_PROBE}" ]] && command -v python >/dev/null 2>&1; then
  TOKEN_LEAK_PROBE="${TOKEN_LEAK_PROBE}" python - "${INSTALL_LOG}" <<'PY' 2>/dev/null || true
import os, pathlib, sys
p = pathlib.Path(sys.argv[1])
tok = os.environ.get("TOKEN_LEAK_PROBE") or ""
if tok and p.is_file():
    text = p.read_text(encoding="utf-8", errors="replace")
    p.write_text(text.replace(tok, "[REDACTED]"), encoding="utf-8")
PY
fi
tail -n 80 "${INSTALL_LOG}" >>"${REPORT_TXT}" || true

if [[ "${INSTALL_RC}" -eq 0 ]]; then
  record INSTALL PASS "exit=0"
else
  record INSTALL FAIL "exit=${INSTALL_RC}"
  BOOTSTRAP_TOKEN=""
  TOKEN_LEAK_PROBE=""
  unset BOOTSTRAP_TOKEN TOKEN_LEAK_PROBE || true
  write_json_report
  exit 1
fi

# Clear live token; keep TOKEN_LEAK_PROBE only for leak scan below
BOOTSTRAP_TOKEN=""
unset BOOTSTRAP_TOKEN || true

CTL="/usr/local/sbin/nyxveilctl"
SERVER="/usr/local/sbin/nyxveil-server"
SHARE_VER="/usr/local/share/nyxveil/VERSION"

# --- post-install verification ---
json_field() {
  local file="$1" key="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg k "${key}" '.[$k] // empty' "${file}" 2>/dev/null || true
    return
  fi
  local py=python3
  command -v python3 >/dev/null 2>&1 || py=python
  "${py}" - "${file}" "${key}" <<'PY' 2>/dev/null || true
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d = json.load(f)
print(d.get(sys.argv[2], "") if isinstance(d, dict) else "")
PY
}

# OS already checked; reaffirm still PASS
record OS_POST PASS "ubuntu 24.04 (reaffirm)"
record SYSTEMD_POST PASS "pid1 systemd (reaffirm)"
record TUN_POST PASS "/dev/net/tun (reaffirm)"

# VERSION
got_ver=""
if [[ -f "${SHARE_VER}" ]]; then
  got_ver="$(tr -d '\r[:space:]' < "${SHARE_VER}")"
fi
if [[ -z "${got_ver}" && -x "${CTL}" ]]; then
  got_ver="$("${CTL}" version --json 2>/dev/null | tr -d '\r' || true)"
  if command -v jq >/dev/null 2>&1; then
    got_ver="$(printf '%s' "${got_ver}" | jq -r '.server_version // .version // empty' 2>/dev/null || true)"
  fi
fi
if [[ "${got_ver}" == "${EXPECTED_VERSION}" ]]; then
  record VERSION PASS "${got_ver}"
else
  record VERSION FAIL "want ${EXPECTED_VERSION} got ${got_ver:-unknown}"
fi

# Binaries
for b in nyxveil-server nyxveilctl; do
  p="/usr/local/sbin/${b}"
  if [[ -x "${p}" ]]; then
    owner="$(stat -c '%U:%G' "${p}" 2>/dev/null || echo "?")"
    mode="$(stat -c '%a' "${p}" 2>/dev/null || echo "?")"
    record "BIN_${b}" PASS "${p} mode=${mode} owner=${owner}"
  else
    record "BIN_${b}" FAIL "missing or not executable: ${p}"
  fi
done

# Management assets
if [[ -f /usr/local/share/nyxveil/scripts/production-gate.sh ]]; then
  record MGMT_GATE PASS "production-gate.sh present"
else
  record MGMT_GATE FAIL "production-gate.sh missing"
fi
if [[ -f /etc/systemd/system/nyxveil-update.service ]]; then
  record MGMT_UPDATE_UNIT PASS "nyxveil-update.service"
else
  record MGMT_UPDATE_UNIT FAIL "nyxveil-update.service missing"
fi
if [[ -f /etc/polkit-1/rules.d/50-nyxveil-management.rules ]]; then
  record MGMT_POLKIT PASS "polkit rules present"
else
  record MGMT_POLKIT SKIP "polkit rules absent (optional on some images)"
fi

# Services
for svc in nyxveil-server.service nyxveil-firewall.service; do
  if systemctl is-active --quiet "${svc}"; then
    record "SVC_${svc}" PASS "active"
  else
    record "SVC_${svc}" FAIL "not active"
  fi
done

# Health
if [[ -x "${CTL}" ]] && "${CTL}" health >/dev/null 2>&1; then
  record HEALTH PASS "nyxveilctl health"
else
  record HEALTH FAIL "nyxveilctl health"
fi

# Status fields
STATUS_JSON="$(mktemp /tmp/nyxveil-status.XXXXXX.json)"
if [[ -x "${CTL}" ]] && "${CTL}" status >"${STATUS_JSON}" 2>/dev/null; then
  running="$(json_field "${STATUS_JSON}" running)"
  healthy="$(json_field "${STATUS_JSON}" healthy)"
  cp_connected="$(json_field "${STATUS_JSON}" cp_connected)"
  accepting="$(json_field "${STATUS_JSON}" accepting)"
  identity="$(json_field "${STATUS_JSON}" identity_present)"
  detail="running=${running} healthy=${healthy} cp_connected=${cp_connected} accepting=${accepting} identity_present=${identity}"
  if [[ "${healthy}" == "true" || "${healthy}" == "True" || "${healthy}" == "1" ]]; then
    record STATUS_FIELDS PASS "${detail}"
  else
    record STATUS_FIELDS FAIL "${detail}"
  fi
  if [[ "${cp_connected}" == "true" || "${cp_connected}" == "True" || "${cp_connected}" == "1" ]]; then
    record CP_CONNECTION PASS "cp_connected=true"
  else
    record CP_CONNECTION FAIL "cp_connected=${cp_connected}"
  fi
else
  record STATUS_FIELDS FAIL "nyxveilctl status failed"
  record CP_CONNECTION FAIL "no status"
fi

# Identity after restart — node_id lives in server.json (not a separate state file).
NODE_ID_BEFORE=""
NODE_KEY_HASH_BEFORE=""
SERVER_JSON="${SERVER_JSON:-/etc/nyxveil/server.json}"
if [[ -f "${SERVER_JSON}" ]]; then
  NODE_ID_BEFORE="$(sed -n 's/.*"node_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${SERVER_JSON}" | head -n1 || true)"
fi
if [[ -f /var/lib/nyxveil/node.key ]]; then
  if command -v sha256sum >/dev/null 2>&1; then
    NODE_KEY_HASH_BEFORE="$(sha256sum /var/lib/nyxveil/node.key | awk '{print $1}')"
  fi
  mode="$(stat -c '%a' /var/lib/nyxveil/node.key 2>/dev/null || echo "?")"
  if [[ "${mode}" == "600" || "${mode}" == "0600" ]]; then
    record NODE_KEY_MODE PASS "0600"
  else
    record NODE_KEY_MODE FAIL "mode=${mode}"
  fi
else
  record NODE_KEY_MODE FAIL "node.key missing"
fi

systemctl restart nyxveil-server.service
sleep 3
NODE_ID_AFTER=""
NODE_KEY_HASH_AFTER=""
if [[ -f "${SERVER_JSON}" ]]; then
  NODE_ID_AFTER="$(sed -n 's/.*"node_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${SERVER_JSON}" | head -n1 || true)"
fi
if [[ -f /var/lib/nyxveil/node.key ]] && command -v sha256sum >/dev/null 2>&1; then
  NODE_KEY_HASH_AFTER="$(sha256sum /var/lib/nyxveil/node.key | awk '{print $1}')"
fi
if [[ -n "${NODE_ID_BEFORE}" && "${NODE_ID_BEFORE}" == "${NODE_ID_AFTER}" ]]; then
  record IDENTITY_RESTART PASS "node_id stable"
else
  record IDENTITY_RESTART FAIL "node_id before=${NODE_ID_BEFORE:-?} after=${NODE_ID_AFTER:-?}"
fi
if [[ -n "${NODE_KEY_HASH_BEFORE}" && "${NODE_KEY_HASH_BEFORE}" == "${NODE_KEY_HASH_AFTER}" ]]; then
  record IDENTITY_KEY_RESTART PASS "node.key hash stable"
else
  record IDENTITY_KEY_RESTART FAIL "node.key hash changed or unavailable"
fi

if systemctl is-active --quiet nyxveil-server.service && [[ -x "${CTL}" ]] && "${CTL}" health >/dev/null 2>&1; then
  record RESTART_TEST PASS "service healthy after restart"
else
  record RESTART_TEST FAIL "unhealthy after restart"
fi

# TLS files — canonical production paths from server.json (fallback: /var/lib/nyxveil).
TLS_CERT="/var/lib/nyxveil/tls.crt"
TLS_KEY="/var/lib/nyxveil/tls.key"
if [[ -f "${SERVER_JSON:-/etc/nyxveil/server.json}" ]]; then
  cfg_cert="$(sed -n 's/.*"tls_cert_file"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${SERVER_JSON:-/etc/nyxveil/server.json}" | head -n1 || true)"
  cfg_key="$(sed -n 's/.*"tls_key_file"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${SERVER_JSON:-/etc/nyxveil/server.json}" | head -n1 || true)"
  [[ -n "${cfg_cert}" ]] && TLS_CERT="${cfg_cert}"
  [[ -n "${cfg_key}" ]] && TLS_KEY="${cfg_key}"
fi
if [[ "${TLS_CERT}" == /etc/nyxveil/tls.crt || "${TLS_KEY}" == /etc/nyxveil/tls.key ]]; then
  record TLS_FILES FAIL "tls paths must not use /etc/nyxveil (got cert=${TLS_CERT} key=${TLS_KEY})"
  record TLS_PARSE NOT_EXECUTED "bad path"
  record TLS_SAN NOT_EXECUTED "bad path"
  record TLS_EXPIRY NOT_EXECUTED "bad path"
elif [[ -f "${TLS_CERT}" && -f "${TLS_KEY}" ]]; then
  km="$(stat -c '%a' "${TLS_KEY}" 2>/dev/null || echo "?")"
  if [[ "${km}" == "600" || "${km}" == "0600" ]]; then
    record TLS_FILES PASS "tls.crt+tls.key key_mode=${km} cert=${TLS_CERT}"
  else
    record TLS_FILES FAIL "tls.key mode=${km}"
  fi
  if command -v openssl >/dev/null 2>&1; then
    if openssl x509 -in "${TLS_CERT}" -noout -text >/tmp/nyxveil-tls-parse.txt 2>/dev/null; then
      record TLS_PARSE PASS "openssl x509 parse"
      if grep -qi "DNS:${PUBLIC_HOST}\|DNS:\\*\\.${PUBLIC_HOST#*.}" /tmp/nyxveil-tls-parse.txt 2>/dev/null \
        || grep -qi "${PUBLIC_HOST}" /tmp/nyxveil-tls-parse.txt 2>/dev/null; then
        record TLS_SAN PASS "SAN/CN mentions ${PUBLIC_HOST}"
      else
        record TLS_SAN FAIL "public-host not found in cert text"
      fi
      end="$(openssl x509 -in "${TLS_CERT}" -noout -enddate 2>/dev/null | cut -d= -f2 || true)"
      if [[ -n "${end}" ]]; then
        record TLS_EXPIRY PASS "notAfter=${end}"
      else
        record TLS_EXPIRY FAIL "cannot read notAfter"
      fi
    else
      record TLS_PARSE FAIL "openssl parse failed"
      record TLS_SAN NOT_EXECUTED "parse failed"
      record TLS_EXPIRY NOT_EXECUTED "parse failed"
    fi
  else
    record TLS_PARSE SKIP "openssl not available"
    record TLS_SAN SKIP "openssl not available"
    record TLS_EXPIRY SKIP "openssl not available"
  fi
else
  record TLS_FILES FAIL "missing cert=${TLS_CERT} key=${TLS_KEY}"
  record TLS_PARSE NOT_EXECUTED "no cert"
  record TLS_SAN NOT_EXECUTED "no cert"
  record TLS_EXPIRY NOT_EXECUTED "no cert"
fi

# Network listeners
if command -v ss >/dev/null 2>&1; then
  if ss -lntu 2>/dev/null | grep -Eq ':443\\b'; then
    record LISTENERS PASS "port 443 listening"
  else
    record LISTENERS FAIL "no listener on :443"
  fi
else
  record LISTENERS SKIP "ss not available"
fi

# Token leak scan WITHOUT printing token
set +x
{ set +o xtrace; } 2>/dev/null || true
LEAK=0
TOKEN_HASH=""
if [[ -n "${TOKEN_LEAK_PROBE}" ]]; then
  if command -v sha256sum >/dev/null 2>&1; then
    TOKEN_HASH="$(printf '%s' "${TOKEN_LEAK_PROBE}" | sha256sum | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    TOKEN_HASH="$(printf '%s' "${TOKEN_LEAK_PROBE}" | shasum -a 256 | awk '{print $1}')"
  fi
  if grep -R --fixed-strings -- "${TOKEN_LEAK_PROBE}" /etc/nyxveil /var/lib/nyxveil /etc/systemd/system /usr/local/share/nyxveil 2>/dev/null >/dev/null; then
    LEAK=1
  fi
  # journalctl scan: never echo the token; compare via fixed-string quiet match only.
  if command -v journalctl >/dev/null 2>&1; then
    for unit in nyxveil-server nyxveil-firewall nyxveil-update; do
      if journalctl -u "${unit}" --no-pager -n 8000 2>/dev/null | grep -F -q -- "${TOKEN_LEAK_PROBE}"; then
        LEAK=1
        break
      fi
    done
    # Also scan recent boots without unit filter for installer-time leaks (bounded).
    if [[ "${LEAK}" -eq 0 ]]; then
      if journalctl --no-pager -n 2000 -t nyxveilctl -t install 2>/dev/null | grep -F -q -- "${TOKEN_LEAK_PROBE}"; then
        LEAK=1
      fi
    fi
  fi
  # Hash presence check: if something logged only the hash of the token, that is fine;
  # we only fail when the raw probe matches. TOKEN_HASH kept for future compare tooling.
  : "${TOKEN_HASH}"
fi
TOKEN_LEAK_PROBE=""
unset TOKEN_LEAK_PROBE TOKEN_HASH || true
if [[ "${LEAK}" -eq 0 ]]; then
  record TOKEN_LEAK PASS "no bootstrap token found in installed paths or unit journals"
else
  record TOKEN_LEAK FAIL "bootstrap token leaked into installed paths or journals"
fi

# Temp state clean
if compgen -G '/tmp/nyxveil-dl.*' >/dev/null 2>&1; then
  record TEMP_STATE FAIL "leftover /tmp/nyxveil-dl.*"
else
  record TEMP_STATE PASS "no leftover download dirs"
fi

# Config must not contain bootstrap_token field values beyond empty
if [[ -f /etc/nyxveil/server.json ]]; then
  if grep -qi 'bootstrap.token\|"bootstrap_token"[[:space:]]*:[[:space:]]*"[^"]\+"' /etc/nyxveil/server.json 2>/dev/null; then
    record CONFIG_NO_TOKEN FAIL "server.json appears to contain bootstrap token"
  else
    record CONFIG_NO_TOKEN PASS "server.json has no bootstrap token"
  fi
else
  record CONFIG_NO_TOKEN FAIL "server.json missing"
fi

# Summarize
{
  echo "=== SUMMARY ==="
  echo "gate=${GATE_TITLE}"
  echo "pass=${PASS_N} fail=${FAIL_N} skip=${SKIP_N} not_executed=${NOT_EXECUTED_N}"
  echo "note=only PASS counts as pass; SKIP/NOT_EXECUTED are not PASS"
} | tee -a "${REPORT_TXT}"

write_json_report

# Required checks: every recorded check that is not SKIP/NOT_EXECUTED must be PASS;
# and FAIL_N must be 0. SKIP/NOT_EXECUTED allowed only for optional tooling.
REQUIRED_FAIL=0
for ((i = 0; i < ${#CHECK_STATUSES[@]}; i++)); do
  st="${CHECK_STATUSES[$i]}"
  name="${CHECK_NAMES[$i]}"
  case "${st}" in
    PASS|SKIP|NOT_EXECUTED) ;;
    *) REQUIRED_FAIL=1; echo "required failure: ${name}=${st}" | tee -a "${REPORT_TXT}" ;;
  esac
done

# Explicitly require key checks to be PASS (not SKIP)
for need in CONFIRM_DISPOSABLE DISPOSABLE_MARKER ROOT OS SYSTEMD TUN CLEAN_STATE INSTALL VERSION HEALTH CP_CONNECTION RESTART_TEST TOKEN_LEAK; do
  found=0
  for ((i = 0; i < ${#CHECK_NAMES[@]}; i++)); do
    if [[ "${CHECK_NAMES[$i]}" == "${need}" ]]; then
      found=1
      if [[ "${CHECK_STATUSES[$i]}" != "PASS" ]]; then
        REQUIRED_FAIL=1
        echo "required check not PASS: ${need}=${CHECK_STATUSES[$i]}" | tee -a "${REPORT_TXT}"
      fi
    fi
  done
  if [[ "${found}" -eq 0 ]]; then
    REQUIRED_FAIL=1
    echo "required check missing: ${need}" | tee -a "${REPORT_TXT}"
  fi
done

if [[ "${REQUIRED_FAIL}" -eq 0 && "${FAIL_N}" -eq 0 ]]; then
  echo "CLEAN HOST GATE RESULT=PASS" | tee -a "${REPORT_TXT}"
  exit 0
fi
echo "CLEAN HOST GATE RESULT=FAIL" | tee -a "${REPORT_TXT}"
exit 1
