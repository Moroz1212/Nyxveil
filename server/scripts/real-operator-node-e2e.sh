#!/usr/bin/env bash
# REAL production-release node E2E on a disposable Ubuntu 24.04 host (systemd PID 1).
#
# Flow (lab Control Plane option C + published server assets):
#   systemd/disposable preflight → lab MSSQL+CP → seed location/bootstrap →
#   install published server-vFROM with --test-self-signed → legacy ACME fixture →
#   Playwright node-update button → wait for TARGET → assert PID change + durable SUCCESS.
#
# This script NEVER emits FULL_OPERATOR_E2E=PASS. It writes partial evidence JSON under
# --evidence-dir. Exit non-zero unless node_button_update and durable_restart are PASS.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LAB_CP_SCRIPT="${SCRIPT_DIR}/lab-control-plane-start.sh"
BROWSER_MJS="${REPO_ROOT}/licensing/scripts/real-node-button-browser.mjs"

FROM_VERSION="${NYXVEIL_FROM_SERVER_VERSION:-1.1.15}"
TARGET_VERSION="${NYXVEIL_TARGET_SERVER_VERSION:-1.1.17}"
GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"
EVIDENCE_DIR=""
WORK_DIR=""
ENABLE_PEBBLE="${NYXVEIL_ENABLE_PEBBLE:-0}"
NODE_NAME="${NYXVEIL_LAB_NODE_NAME:-lab-node-e2e}"
PUBLIC_HOST="${NYXVEIL_LAB_PUBLIC_HOST:-127.0.0.1}"
TLS_PORT="${NYXVEIL_LAB_TLS_PORT:-8443}"
QUIC_PORT="${NYXVEIL_LAB_QUIC_PORT:-8443}"

NODE_BUTTON_RESULT=FAIL
DURABLE_RESTART_RESULT=FAIL
SYSTEMD_PREFLIGHT_RESULT=FAIL
FINAL_EXIT=1
BLOCKED=0

usage() {
  cat <<'EOF'
Usage: real-operator-node-e2e.sh --evidence-dir DIR [--work-dir DIR]

Env:
  NYXVEIL_FROM_SERVER_VERSION   default 1.1.15 (published install tag)
  NYXVEIL_TARGET_SERVER_VERSION default 1.1.17
  NYXVEIL_DISPOSABLE_HOST_ALLOW=1  OR touch /root/NYXVEIL_DISPOSABLE_TEST_HOST
  NYXVEIL_ENABLE_PEBBLE=1          optionally start Pebble (ACME gates still NOT_EXECUTED)
  GH_TOKEN                         used by gh release download when set
EOF
}

die() { echo "real-operator-node-e2e: ERROR: $*" >&2; exit 1; }
log() { echo "real-operator-node-e2e: $*"; }
blocked() {
  echo "real-operator-node-e2e: BLOCKED: $*" >&2
  BLOCKED=1
  write_evidence_placeholders "BLOCKED"
  echo "NODE_OPERATOR_E2E=BLOCKED"
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --evidence-dir) EVIDENCE_DIR="${2:-}"; shift 2 ;;
    --work-dir) WORK_DIR="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "${EVIDENCE_DIR}" ]] || die "--evidence-dir required"
mkdir -p "${EVIDENCE_DIR}"
EVIDENCE_DIR="$(cd "${EVIDENCE_DIR}" && pwd)"

if [[ -z "${WORK_DIR}" ]]; then
  WORK_DIR="$(mktemp -d /tmp/nyxveil-real-node-e2e.XXXXXX)"
fi
mkdir -p "${WORK_DIR}"
WORK_DIR="$(cd "${WORK_DIR}" && pwd)"

write_json() {
  local path="$1"
  shift
  python3 - "$path" "$@" <<'PY'
import json, sys
path = sys.argv[1]
# remaining argv: key=value pairs; values may be JSON literals if prefixed with json:
obj = {}
for arg in sys.argv[2:]:
    k, _, v = arg.partition("=")
    if v.startswith("json:"):
        obj[k] = json.loads(v[5:])
    else:
        obj[k] = v
with open(path, "w", encoding="utf-8") as f:
    json.dump(obj, f, indent=2, sort_keys=True)
    f.write("\n")
PY
}

write_evidence_placeholders() {
  local systemd_result="${1:-NOT_EXECUTED}"
  write_json "${EVIDENCE_DIR}/systemd_preflight-evidence.json" \
    gate=systemd_preflight result="${systemd_result}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_json "${EVIDENCE_DIR}/node_button_update-evidence.json" \
    gate=node_button_update result="${NODE_BUTTON_RESULT}" \
    from_version="${FROM_VERSION}" target_version="${TARGET_VERSION}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_json "${EVIDENCE_DIR}/durable_restart-evidence.json" \
    gate=durable_restart result="${DURABLE_RESTART_RESULT}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for gate in acme_pebble cert_button tls_served quic_handshake rollback_recovery; do
    write_json "${EVIDENCE_DIR}/${gate}-evidence.json" \
      gate="${gate}" result=NOT_EXECUTED \
      note="not run by real-operator-node-e2e.sh" \
      finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  done
}

FINISHED=0
SIBLING_REFRESH_PID=""
finish() {
  if [[ "${FINISHED}" -eq 1 ]]; then
    return 0
  fi
  FINISHED=1
  [[ -n "${SIBLING_REFRESH_PID}" ]] && kill "${SIBLING_REFRESH_PID}" 2>/dev/null || true
  write_json "${EVIDENCE_DIR}/systemd_preflight-evidence.json" \
    gate=systemd_preflight result="${SYSTEMD_PREFLIGHT_RESULT}" \
    pid1_comm="${PID1_COMM:-}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_json "${EVIDENCE_DIR}/node_button_update-evidence.json" \
    gate=node_button_update result="${NODE_BUTTON_RESULT}" \
    from_version="${FROM_VERSION}" target_version="${TARGET_VERSION}" \
    old_pid="${OLD_PID:-}" new_pid="${NEW_PID:-}" node_id="${NODE_ID:-}" \
    clicked_at="${CLICKED_AT:-}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_json "${EVIDENCE_DIR}/durable_restart-evidence.json" \
    gate=durable_restart result="${DURABLE_RESTART_RESULT}" \
    old_pid="${OLD_PID:-}" new_pid="${NEW_PID:-}" \
    version_after="${VERSION_AFTER:-}" \
    command_status="${COMMAND_STATUS:-}" command_result_code="${COMMAND_RESULT_CODE:-}" \
    marker_path="/var/lib/nyxveil/management/update-command.json" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for gate in acme_pebble cert_button tls_served quic_handshake rollback_recovery; do
    if [[ ! -f "${EVIDENCE_DIR}/${gate}-evidence.json" ]]; then
      write_json "${EVIDENCE_DIR}/${gate}-evidence.json" \
        gate="${gate}" result=NOT_EXECUTED \
        note="not run by real-operator-node-e2e.sh" \
        finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
  done
  if [[ "${BLOCKED}" -eq 1 ]]; then
    echo "NODE_OPERATOR_E2E=BLOCKED"
    exit 2
  fi
  if [[ "${NODE_BUTTON_RESULT}" == "PASS" && "${DURABLE_RESTART_RESULT}" == "PASS" ]]; then
    echo "NODE_OPERATOR_E2E=PASS"
    echo "NOTE=This script alone does not authorize FULL_OPERATOR_E2E=PASS"
    FINAL_EXIT=0
  else
    echo "NODE_OPERATOR_E2E=FAIL node_button_update=${NODE_BUTTON_RESULT} durable_restart=${DURABLE_RESTART_RESULT}"
    FINAL_EXIT=1
  fi
  exit "${FINAL_EXIT}"
}
trap finish EXIT

# --- 1) systemd PID 1 ----------------------------------------------------------
PID1_COMM="$(ps -p 1 -o comm= 2>/dev/null | tr -d '[:space:]' || true)"
if [[ "${PID1_COMM}" != "systemd" ]]; then
  SYSTEMD_PREFLIGHT_RESULT=FAIL
  blocked "PID 1 is '${PID1_COMM:-unknown}', require systemd (disposable Ubuntu 24.04)"
fi
SYSTEMD_PREFLIGHT_RESULT=PASS
log "systemd PID 1 OK"

# --- 2) disposable host marker -------------------------------------------------
if [[ -f /root/NYXVEIL_DISPOSABLE_TEST_HOST ]]; then
  log "disposable marker /root/NYXVEIL_DISPOSABLE_TEST_HOST present"
elif [[ "${NYXVEIL_DISPOSABLE_HOST_ALLOW:-0}" == "1" ]]; then
  log "disposable allow via NYXVEIL_DISPOSABLE_HOST_ALLOW=1"
  mkdir -p /root
  touch /root/NYXVEIL_DISPOSABLE_TEST_HOST || true
else
  blocked "need /root/NYXVEIL_DISPOSABLE_TEST_HOST or NYXVEIL_DISPOSABLE_HOST_ALLOW=1"
fi

[[ "$(id -u)" -eq 0 ]] || die "must run as root (install/systemd)"
[[ -x "${LAB_CP_SCRIPT}" || -f "${LAB_CP_SCRIPT}" ]] || die "missing ${LAB_CP_SCRIPT}"
[[ -f "${BROWSER_MJS}" ]] || die "missing ${BROWSER_MJS}"
command -v curl >/dev/null 2>&1 || die "curl required"
command -v python3 >/dev/null 2>&1 || die "python3 required"
command -v openssl >/dev/null 2>&1 || die "openssl required"
command -v docker >/dev/null 2>&1 || die "docker required"
command -v dotnet >/dev/null 2>&1 || die "dotnet required"
command -v node >/dev/null 2>&1 || die "node required"
command -v npm >/dev/null 2>&1 || die "npm required"
command -v jq >/dev/null 2>&1 || die "jq required"
command -v systemctl >/dev/null 2>&1 || die "systemctl required"

if command -v gh >/dev/null 2>&1; then
  GH=(gh)
else
  die "gh CLI required for release download"
fi
if [[ -n "${GH_TOKEN:-}" ]]; then
  export GH_TOKEN
  export GH_ENTERPRISE_TOKEN="${GH_ENTERPRISE_TOKEN:-}"
fi

# --- 3/4/5/6) lab Control Plane ------------------------------------------------
CP_ENV="${WORK_DIR}/lab-cp.env"
bash "${LAB_CP_SCRIPT}" --work-dir "${WORK_DIR}/lab-cp" --output-env "${CP_ENV}"
# shellcheck disable=SC1090
set -a
source "${CP_ENV}"
set +a
[[ -n "${CP_BASE:-}" && -n "${CP_BOOTSTRAP_TOKEN:-}" && -n "${CP_CA_PEM:-}" ]] || die "lab CP env incomplete"

sql_q() {
  local q="$1"
  if docker exec "${CP_MSSQL_CONTAINER}" test -x /opt/mssql-tools18/bin/sqlcmd; then
    docker exec "${CP_MSSQL_CONTAINER}" /opt/mssql-tools18/bin/sqlcmd \
      -S localhost -U sa -P "${CP_SA_PASSWORD}" -C -d "${CP_DB_NAME}" -h -1 -W -Q "${q}"
  else
    docker exec "${CP_MSSQL_CONTAINER}" /opt/mssql-tools/bin/sqlcmd \
      -S localhost -U sa -P "${CP_SA_PASSWORD}" -C -d "${CP_DB_NAME}" -h -1 -W -Q "${q}"
  fi
}

# Keep a fake healthy sibling so location-safety allows disruptive update on a single real node.
seed_and_refresh_sibling() {
  local pubhex now
  # PublicIdentity CHECK requires exactly 32 bytes (64 hex digits).
  pubhex="$(openssl rand -hex 32)"
  now="$(date -u +'%Y-%m-%d %H:%M:%S')"
  sql_q "
IF NOT EXISTS (SELECT 1 FROM Nodes WHERE NodeId=N'lab-sibling-healthy')
BEGIN
  INSERT INTO Nodes (
    NodeId, LocationId, DisplayName, Status, LifecycleState, Enabled, TestOnly, Draining,
    ProtocolVersion, Capacity, CurrentSessions, PublicIdentity, CreatedAt, UpdatedAt, ConfigVersion, LastSeenAt
  ) VALUES (
    N'lab-sibling-healthy', N'${CP_LOCATION_ID}', N'Lab Sibling (synthetic)', 0, 0, 1, 0, 0,
    1, 100, 0, CONVERT(varbinary(32), 0x${pubhex}), '${now}', '${now}', 1, '${now}'
  );
END
ELSE
BEGIN
  UPDATE Nodes SET LastSeenAt='${now}', Status=0, LifecycleState=0, Enabled=1, TestOnly=0, Draining=0,
    Capacity=100, CurrentSessions=0, UpdatedAt='${now}'
  WHERE NodeId=N'lab-sibling-healthy';
END
IF NOT EXISTS (SELECT 1 FROM NodeConfigs WHERE NodeId=N'lab-sibling-healthy')
BEGIN
  INSERT INTO NodeConfigs (NodeId, Enabled, Draining, MaintenanceMode, TransportPolicyJson, Capacity, ConfigVersion, UpdatedAt)
  VALUES (N'lab-sibling-healthy', 1, 0, 0, N'{}', 100, 1, '${now}');
END
ELSE
BEGIN
  UPDATE NodeConfigs SET Enabled=1, Draining=0, MaintenanceMode=0, Capacity=100, UpdatedAt='${now}'
  WHERE NodeId=N'lab-sibling-healthy';
END
" >/dev/null
}

seed_and_refresh_sibling
(
  while true; do
    sleep 60
    seed_and_refresh_sibling || true
  done
) &
SIBLING_REFRESH_PID=$!

# --- 7) download published FROM assets + install --------------------------------
ASSET_DIR="${WORK_DIR}/server-v${FROM_VERSION}"
mkdir -p "${ASSET_DIR}"
log "downloading published server-v${FROM_VERSION} assets"
"${GH[@]}" release download "server-v${FROM_VERSION}" -R "${GITHUB_REPO}" -D "${ASSET_DIR}" --clobber

[[ -f "${ASSET_DIR}/SHA256SUMS" ]] || die "SHA256SUMS missing from server-v${FROM_VERSION}"
[[ -f "${ASSET_DIR}/install.sh" ]] || die "install.sh missing from server-v${FROM_VERSION}"
(
  cd "${ASSET_DIR}"
  tr -d '\r' < SHA256SUMS | sha256sum -c - >/dev/null
)
log "SHA256SUMS verification PASS for server-v${FROM_VERSION}"
chmod 0755 "${ASSET_DIR}/install.sh"

# Optional Pebble (ACME evidence remains NOT_EXECUTED unless a later cert path runs)
PEBBLE_DIR_URL=""
if [[ "${ENABLE_PEBBLE}" == "1" ]]; then
  log "starting Pebble ACME lab CA (optional)"
  if ! docker ps --format '{{.Names}}' | grep -qx nyxveil-lab-pebble; then
    docker rm -f nyxveil-lab-pebble >/dev/null 2>&1 || true
    docker run -d --name nyxveil-lab-pebble \
      -p 14000:14000 -p 15000:15000 \
      ghcr.io/letsencrypt/pebble:latest \
      pebble -config /test/config/pebble-config.json -dnsserver 127.0.0.1:8053 \
      >/dev/null 2>&1 || \
    docker run -d --name nyxveil-lab-pebble \
      -p 14000:14000 \
      letsencrypt/pebble:latest >/dev/null 2>&1 || \
      log "WARN: could not start Pebble image; continuing without ACME lab"
  fi
  if docker ps --format '{{.Names}}' | grep -qx nyxveil-lab-pebble; then
    PEBBLE_DIR_URL="https://127.0.0.1:14000/dir"
    write_json "${EVIDENCE_DIR}/acme_pebble-evidence.json" \
      gate=acme_pebble result=NOT_EXECUTED \
      note="Pebble container started but cert_button/tls_served/quic not exercised by this script" \
      pebble_directory="${PEBBLE_DIR_URL}" \
      finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  fi
fi

# Do not attach --tls-domain/ACME on the FROM install: --test-self-signed is the lab TLS mode.
# Pebble may be running for a later optional ACME path; cert_button remains NOT_EXECUTED here.
INSTALL_ARGS=(
  --control-plane "${CP_BASE}"
  --control-plane-ca-file "${CP_CA_PEM}"
  --location "${CP_LOCATION_ID}"
  --name "${NODE_NAME}"
  --public-host "${PUBLIC_HOST}"
  --tls-port "${TLS_PORT}"
  --quic-port "${QUIC_PORT}"
  --test-self-signed
  --non-interactive
  --bootstrap-stdin
)

log "installing published server-v${FROM_VERSION} with --test-self-signed"
# Prefer packaged install.sh DEFAULT_RELEASE_VERSION; also pin env for clarity.
printf '%s\n' "${CP_BOOTSTRAP_TOKEN}" | \
  NYXVEIL_VERSION="${FROM_VERSION}" \
  bash "${ASSET_DIR}/install.sh" "${INSTALL_ARGS[@]}"

systemctl daemon-reload || true
systemctl enable --now nyxveil-server
sleep 3
systemctl is-active --quiet nyxveil-server || die "nyxveil-server not active after install"

VERSION_BEFORE="$(tr -d '\r[:space:]' </usr/local/share/nyxveil/VERSION 2>/dev/null || true)"
[[ "${VERSION_BEFORE}" == "${FROM_VERSION}" ]] || die "installed VERSION want=${FROM_VERSION} have=${VERSION_BEFORE}"
NODE_ID="$(jq -r '.node_id // empty' /etc/nyxveil/server.json)"
[[ -n "${NODE_ID}" ]] || die "node_id missing from /etc/nyxveil/server.json"
log "installed node_id=${NODE_ID} version=${VERSION_BEFORE}"

# Progress-lease long-run: DeliveryTtl is short on lab CP; delay update past that window.
mkdir -p /etc/systemd/system/nyxveil-update.service.d
cat >/etc/systemd/system/nyxveil-update.service.d/lab-delay.conf <<EOF
[Service]
Environment=NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS=${NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS:-25}
Environment=NYXVEIL_ACME_INSECURE_DIRECTORY_TLS=1
EOF
systemctl daemon-reload

# Wait until CP sees the node + capabilities (heartbeat)
for i in $(seq 1 60); do
  got="$(sql_q "SET NOCOUNT ON; SELECT TOP 1 ISNULL(ReportedServerVersion,''), ISNULL(ManagementCapabilities,''), SupportsNodeCommands FROM Nodes WHERE NodeId=N'${NODE_ID}';" \
    | tr -d '\r' | sed '/^$/d' | head -n1 || true)"
  if echo "${got}" | grep -qi 'node_update'; then
    log "node heartbeat reports node_update capability"
    break
  fi
  if [[ "${i}" -eq 60 ]]; then
    die "timed out waiting for node heartbeat/capabilities: ${got}"
  fi
  sleep 5
done

# --- 12) legacy ACME fixture BEFORE update (while still on FROM) ----------------
log "creating legacy ACME fixture root:root 0700 on /var/lib/nyxveil/acme"
mkdir -p /var/lib/nyxveil/acme
chown root:root /var/lib/nyxveil/acme
chmod 0700 /var/lib/nyxveil/acme
# Drop a sentinel so migration ownership change is observable if a later gate checks it.
if [[ ! -f /var/lib/nyxveil/acme/acme-account.key ]]; then
  umask 077
  printf 'legacy-fixture\n' >/var/lib/nyxveil/acme/acme-account.key
  chown root:root /var/lib/nyxveil/acme/acme-account.key
  chmod 0600 /var/lib/nyxveil/acme/acme-account.key
fi

# --- 8) record old PID ---------------------------------------------------------
OLD_PID="$(systemctl show -p MainPID --value nyxveil-server | tr -d '[:space:]')"
[[ -n "${OLD_PID}" && "${OLD_PID}" != "0" ]] || die "could not read MainPID for nyxveil-server"
log "old MainPID=${OLD_PID}"

seed_and_refresh_sibling

# --- 9) Playwright button click ------------------------------------------------
PLAY_DIR="${WORK_DIR}/playwright"
mkdir -p "${PLAY_DIR}"
cp -a "${BROWSER_MJS}" "${PLAY_DIR}/real-node-button-browser.mjs"
(
  cd "${PLAY_DIR}"
  npm init -y >/dev/null 2>&1
  npm install playwright@1.49.1 >/dev/null
  npx playwright install chromium >/dev/null
  export CP_BASE CP_EMAIL="${CP_ADMIN_USER}" CP_PASSWORD="${CP_ADMIN_PASSWORD}"
  export CP_NODE_ID="${NODE_ID}" CP_TARGET_VERSION="${TARGET_VERSION}"
  export CP_TOTP_SECRET=""
  export CP_TOTP_OUT="${WORK_DIR}/totp-secret.txt"
  export CP_CLICK_MARKER="${WORK_DIR}/node-click.marker"
  node real-node-button-browser.mjs
)
CLICKED_AT="$(tr -d '\r' <"${WORK_DIR}/node-click.marker" 2>/dev/null | head -n1 || true)"
[[ -n "${CLICKED_AT}" ]] || die "Playwright did not write click marker"
NODE_BUTTON_RESULT=PASS
log "browser node-update click recorded"

# --- 10/11) wait for TARGET + durable SUCCESS + PID change ----------------------
log "waiting for update to ${TARGET_VERSION} (durable SUCCESS + PID change)"
DEADLINE=$(( $(date +%s) + 1800 ))
COMMAND_STATUS=""
COMMAND_RESULT_CODE=""
VERSION_AFTER=""
NEW_PID=""

while [[ "$(date +%s)" -lt "${DEADLINE}" ]]; do
  seed_and_refresh_sibling || true
  VERSION_AFTER="$(tr -d '\r[:space:]' </usr/local/share/nyxveil/VERSION 2>/dev/null || true)"
  NEW_PID="$(systemctl show -p MainPID --value nyxveil-server 2>/dev/null | tr -d '[:space:]' || true)"
  row="$(sql_q "SET NOCOUNT ON; SELECT TOP 1 Status, ISNULL(ResultCode,''), ISNULL(TargetVersion,'') FROM NodeCommands WHERE NodeId=N'${NODE_ID}' AND Type=3 ORDER BY CreatedAt DESC;" \
    | tr -d '\r' | sed '/^$/d' | head -n1 || true)"
  # Status Succeeded=3
  COMMAND_STATUS="$(awk '{print $1}' <<<"${row}")"
  COMMAND_RESULT_CODE="$(awk '{print $2}' <<<"${row}")"
  if [[ "${VERSION_AFTER}" == "${TARGET_VERSION}" \
     && -n "${NEW_PID}" && "${NEW_PID}" != "0" && "${NEW_PID}" != "${OLD_PID}" \
     && "${COMMAND_STATUS}" == "3" \
     && "${COMMAND_RESULT_CODE}" == "updated_healthy" ]]; then
    DURABLE_RESTART_RESULT=PASS
    log "durable update PASS version=${VERSION_AFTER} old_pid=${OLD_PID} new_pid=${NEW_PID} result=${COMMAND_RESULT_CODE}"
    break
  fi
  sleep 10
done

if [[ "${DURABLE_RESTART_RESULT}" != "PASS" ]]; then
  log "durable wait timed out version=${VERSION_AFTER} pid=${NEW_PID} status=${COMMAND_STATUS} code=${COMMAND_RESULT_CODE}"
fi

# Marker path note (may be deleted after success on some versions)
if [[ -f /var/lib/nyxveil/management/update-command.json ]]; then
  log "update-command marker still present (non-fatal)"
fi

exit 0
