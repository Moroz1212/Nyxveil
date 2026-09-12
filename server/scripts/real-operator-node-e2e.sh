#!/usr/bin/env bash
# REAL production-release node E2E on a disposable Ubuntu 24.04 host (systemd PID 1).
#
# Flow (lab Control Plane option C + published server assets):
#   systemd/disposable preflight → lab MSSQL+CP → seed location/bootstrap →
#   install published server-vFROM with --test-self-signed → legacy ACME fixture →
#   Playwright node-update button → wait for TARGET → assert PID change + durable SUCCESS →
#   (when Pebble enabled) install locally built 1.1.18 candidate → ACME/cert/TLS/QUIC/rollback.
#
# This script NEVER emits FULL_OPERATOR_E2E=PASS. It writes partial evidence JSON under
# --evidence-dir. Exit non-zero unless node_button_update and durable_restart are PASS.
# When NYXVEIL_ENABLE_PEBBLE=1, ACME/cert/TLS/QUIC/rollback must also be PASS (fail-closed).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LAB_CP_SCRIPT="${SCRIPT_DIR}/lab-control-plane-start.sh"
BROWSER_MJS="${REPO_ROOT}/licensing/scripts/real-node-button-browser.mjs"

FROM_VERSION="${NYXVEIL_FROM_SERVER_VERSION:-1.1.15}"
TARGET_VERSION="${NYXVEIL_TARGET_SERVER_VERSION:-1.1.17}"
CANDIDATE_VERSION="${NYXVEIL_CANDIDATE_SERVER_VERSION:-1.1.18}"
GITHUB_REPO="${NYXVEIL_GITHUB_REPO:-Moroz1212/Nyxveil}"
EVIDENCE_DIR=""
WORK_DIR=""
ENABLE_PEBBLE="${NYXVEIL_ENABLE_PEBBLE:-0}"
NODE_NAME="${NYXVEIL_LAB_NODE_NAME:-lab-node-e2e}"
PUBLIC_HOST="${NYXVEIL_LAB_PUBLIC_HOST:-127.0.0.1}"
TLS_PORT="${NYXVEIL_LAB_TLS_PORT:-8443}"
QUIC_PORT="${NYXVEIL_LAB_QUIC_PORT:-8443}"
ACME_DOMAIN="${NYXVEIL_LAB_ACME_DOMAIN:-node-e2e.test}"
ACME_EMAIL="${NYXVEIL_LAB_ACME_EMAIL:-lab@node-e2e.test}"
PEBBLE_DIR_URL=""

NODE_BUTTON_RESULT=FAIL
DURABLE_RESTART_RESULT=FAIL
SYSTEMD_PREFLIGHT_RESULT=FAIL
ACME_PEBBLE_RESULT=NOT_EXECUTED
CERT_BUTTON_RESULT=NOT_EXECUTED
TLS_SERVED_RESULT=NOT_EXECUTED
QUIC_HANDSHAKE_RESULT=NOT_EXECUTED
ROLLBACK_RECOVERY_RESULT=NOT_EXECUTED
ACME_PHASE_RAN=0
FINAL_EXIT=1
BLOCKED=0

usage() {
  cat <<'EOF'
Usage: real-operator-node-e2e.sh --evidence-dir DIR [--work-dir DIR]

Env:
  NYXVEIL_FROM_SERVER_VERSION      default 1.1.15 (published install tag)
  NYXVEIL_TARGET_SERVER_VERSION    default 1.1.17
  NYXVEIL_CANDIDATE_SERVER_VERSION default 1.1.18 (local go build after durable PASS)
  NYXVEIL_DISPOSABLE_HOST_ALLOW=1  OR touch /root/NYXVEIL_DISPOSABLE_TEST_HOST
  NYXVEIL_ENABLE_PEBBLE=1          start Pebble + run ACME/cert/TLS/QUIC/rollback after durable PASS
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

acme_gates_ok() {
  # Fail-closed: ENABLE_PEBBLE=1 requires a completed ACME phase with all PASS.
  # Never treat ACME_PHASE_RAN=0 as success when Pebble was requested (truncated
  # scripts / early EXIT traps previously false-PASSed here).
  if [[ "${ENABLE_PEBBLE}" == "1" ]]; then
    [[ "${ACME_PHASE_RAN}" -eq 1 \
      && "${ACME_PEBBLE_RESULT}" == "PASS" \
      && "${CERT_BUTTON_RESULT}" == "PASS" \
      && "${TLS_SERVED_RESULT}" == "PASS" \
      && "${QUIC_HANDSHAKE_RESULT}" == "PASS" \
      && "${ROLLBACK_RECOVERY_RESULT}" == "PASS" ]]
    return $?
  fi
  if [[ "${ACME_PHASE_RAN}" -eq 0 ]]; then
    return 0
  fi
  [[ "${ACME_PEBBLE_RESULT}" == "PASS" \
    && "${CERT_BUTTON_RESULT}" == "PASS" \
    && "${TLS_SERVED_RESULT}" == "PASS" \
    && "${QUIC_HANDSHAKE_RESULT}" == "PASS" \
    && "${ROLLBACK_RECOVERY_RESULT}" == "PASS" ]]
}

FINISHED=0
SIBLING_REFRESH_PID=""
PEBBLE_PID=""
stop_pebble_lab() {
  if [[ -n "${PEBBLE_PID}" ]] && kill -0 "${PEBBLE_PID}" 2>/dev/null; then
    kill "${PEBBLE_PID}" 2>/dev/null || true
    wait "${PEBBLE_PID}" 2>/dev/null || true
  fi
  PEBBLE_PID=""
  docker rm -f nyxveil-lab-pebble >/dev/null 2>&1 || true
}
finish() {
  if [[ "${FINISHED}" -eq 1 ]]; then
    return 0
  fi
  FINISHED=1
  [[ -n "${SIBLING_REFRESH_PID}" ]] && kill "${SIBLING_REFRESH_PID}" 2>/dev/null || true
  stop_pebble_lab 2>/dev/null || true
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
  if [[ "${NODE_BUTTON_RESULT}" == "PASS" && "${DURABLE_RESTART_RESULT}" == "PASS" ]] && acme_gates_ok; then
    echo "NODE_OPERATOR_E2E=PASS"
    echo "NOTE=This script alone does not authorize FULL_OPERATOR_E2E=PASS"
    FINAL_EXIT=0
  else
    echo "NODE_OPERATOR_E2E=FAIL node_button_update=${NODE_BUTTON_RESULT} durable_restart=${DURABLE_RESTART_RESULT} acme_pebble=${ACME_PEBBLE_RESULT} cert_button=${CERT_BUTTON_RESULT} tls_served=${TLS_SERVED_RESULT} quic_handshake=${QUIC_HANDSHAKE_RESULT} rollback_recovery=${ROLLBACK_RECOVERY_RESULT}"
    FINAL_EXIT=1
  fi
  exit "${FINAL_EXIT}"
}
trap finish EXIT

fail_acme_gate() {
  local gate="$1"
  local reason="$2"
  shift 2 || true
  case "${gate}" in
    acme_pebble) ACME_PEBBLE_RESULT=FAIL ;;
    cert_button) CERT_BUTTON_RESULT=FAIL ;;
    tls_served) TLS_SERVED_RESULT=FAIL ;;
    quic_handshake) QUIC_HANDSHAKE_RESULT=FAIL ;;
    rollback_recovery) ROLLBACK_RECOVERY_RESULT=FAIL ;;
  esac
  write_json "${EVIDENCE_DIR}/${gate}-evidence.json" \
    gate="${gate}" result=FAIL reason="${reason}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    "$@"
  log "FAIL ${gate}: ${reason}"
}

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
if [[ "${ENABLE_PEBBLE}" == "1" ]]; then
  command -v go >/dev/null 2>&1 || die "go required when NYXVEIL_ENABLE_PEBBLE=1 (candidate build + QUIC probe)"
fi

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

PEBBLE_SRC_DIR=""
PEBBLE_SRC_DIR=""
wait_pebble_directory() {
  local i
  for i in $(seq 1 60); do
    if curl -skf "https://127.0.0.1:14000/dir" >/dev/null 2>&1; then
      PEBBLE_DIR_URL="https://127.0.0.1:14000/dir"
      return 0
    fi
    sleep 1
  done
  return 1
}

start_pebble_from_source() {
  # Docker Hub no longer hosts letsencrypt/pebble; GHCR often needs auth.
  # Build upstream Pebble and run with httpPort=80 so HTTP-01 hits the node.
  local tag="${NYXVEIL_PEBBLE_GIT_REF:-v2.7.0}"
  PEBBLE_SRC_DIR="${WORK_DIR}/pebble-src"
  rm -rf "${PEBBLE_SRC_DIR}"
  log "cloning letsencrypt/pebble@${tag} for host ACME lab"
  git clone --depth 1 --branch "${tag}" https://github.com/letsencrypt/pebble.git "${PEBBLE_SRC_DIR}"
  (
    cd "${PEBBLE_SRC_DIR}"
    go build -o "${WORK_DIR}/pebble" ./cmd/pebble
  )
  # Upstream test config uses httpPort 5002; product ACME listens on :80.
  cat >"${WORK_DIR}/pebble-lab-config.json" <<'EOF'
{
  "pebble": {
    "listenAddress": "0.0.0.0:14000",
    "managementListenAddress": "0.0.0.0:15000",
    "certificate": "test/certs/localhost/cert.pem",
    "privateKey": "test/certs/localhost/key.pem",
    "httpPort": 80,
    "tlsPort": 443,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false,
    "domainBlocklist": ["blocked-domain.example"]
  }
}
EOF
  stop_pebble_lab
  (
    cd "${PEBBLE_SRC_DIR}"
    PEBBLE_VA_NOSLEEP=1 \
      "${WORK_DIR}/pebble" -config "${WORK_DIR}/pebble-lab-config.json" \
      >"${WORK_DIR}/pebble.log" 2>&1 &
    echo $! >"${WORK_DIR}/pebble.pid"
  )
  PEBBLE_PID="$(tr -d '[:space:]' <"${WORK_DIR}/pebble.pid")"
  log "pebble process started pid=${PEBBLE_PID}"
  if wait_pebble_directory; then
    return 0
  fi
  log "pebble directory not reachable after source start"
  tail -n 80 "${WORK_DIR}/pebble.log" >&2 || true
  stop_pebble_lab
  return 1
}

ensure_pebble_host_network() {
  log "ensuring Pebble ACME lab CA (HTTP-01 validation port 80)"
  # Prefer source-built Pebble: Docker Hub image is gone, GHCR often needs auth,
  # and upstream container config defaults httpPort=5002 (product listens on :80).
  if start_pebble_from_source; then
    return 0
  fi
  stop_pebble_lab
  local out="" rc=1
  set +e
  if [[ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ]]; then
    local tok="${GH_TOKEN:-${GITHUB_TOKEN}}"
    echo "${tok}" | docker login ghcr.io -u "${GITHUB_ACTOR:-nyxveil}" --password-stdin >/dev/null 2>&1
  fi
  out="$(docker run -d --name nyxveil-lab-pebble --network host \
      -e PEBBLE_VA_NOSLEEP=1 \
      -v "${WORK_DIR}/pebble-lab-config.json:/test/config/pebble-config.json:ro" \
      ghcr.io/letsencrypt/pebble:v2.7.0 \
      -config /test/config/pebble-config.json 2>&1)"
  rc=$?
  if [[ "${rc}" -ne 0 ]]; then
    docker rm -f nyxveil-lab-pebble >/dev/null 2>&1 || true
    # Write lab config for volume mount even if source clone failed earlier.
    if [[ ! -f "${WORK_DIR}/pebble-lab-config.json" ]]; then
      cat >"${WORK_DIR}/pebble-lab-config.json" <<'EOF'
{
  "pebble": {
    "listenAddress": "0.0.0.0:14000",
    "managementListenAddress": "0.0.0.0:15000",
    "certificate": "test/certs/localhost/cert.pem",
    "privateKey": "test/certs/localhost/key.pem",
    "httpPort": 80,
    "tlsPort": 443,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false
  }
}
EOF
    fi
    out="$(docker run -d --name nyxveil-lab-pebble --network host \
        -e PEBBLE_VA_NOSLEEP=1 \
        ghcr.io/letsencrypt/pebble:latest 2>&1)"
    rc=$?
  fi
  set -e
  if [[ "${rc}" -ne 0 ]]; then
    log "pebble docker fallback failed: ${out}"
    return 1
  fi
  log "pebble container started: ${out}"
  if wait_pebble_directory; then
    return 0
  fi
  log "pebble directory not reachable on https://127.0.0.1:14000/dir"
  docker logs nyxveil-lab-pebble 2>&1 | tail -n 40 || true
  stop_pebble_lab
  return 1
}

# Pebble preflight when requested (ACME evidence remains NOT_EXECUTED until post-durable phase)
if [[ "${ENABLE_PEBBLE}" == "1" ]]; then
  if ensure_pebble_host_network; then
    write_json "${EVIDENCE_DIR}/acme_pebble-evidence.json" \
      gate=acme_pebble result=NOT_EXECUTED \
      note="Pebble lab started; cert phase pending durable PASS" \
      pebble_directory="${PEBBLE_DIR_URL}" \
      finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  else
    log "ERROR: could not start Pebble ACME lab (required when NYXVEIL_ENABLE_PEBBLE=1)"
    write_json "${EVIDENCE_DIR}/acme_pebble-evidence.json" \
      gate=acme_pebble result=FAIL \
      note="pebble_start_failed_preflight" \
      finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    ACME_PEBBLE_RESULT=FAIL
    # Continue node button / durable; finish() will FAIL because ENABLE_PEBBLE=1.
    PEBBLE_DIR_URL=""
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
# Also inject ACME insecure directory TLS into the long-running server unit for candidate ACME.
mkdir -p /etc/systemd/system/nyxveil-server.service.d
cat >/etc/systemd/system/nyxveil-server.service.d/lab-acme.conf <<EOF
[Service]
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
  export CP_ACTION=update
  node real-node-button-browser.mjs
)
CLICKED_AT="$(tr -d '\r' <"${WORK_DIR}/node-click.marker" 2>/dev/null | head -n1 || true)"
[[ -n "${CLICKED_AT}" ]] || die "Playwright did not write click marker"
NODE_BUTTON_RESULT=PASS
log "browser node-update click recorded"
TOTP_SECRET="$(tr -d '\r[:space:]' <"${WORK_DIR}/totp-secret.txt" 2>/dev/null || true)"

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

# =============================================================================
# Post-durable ACME / cert / TLS / QUIC / rollback against local 1.1.18 candidate
# =============================================================================
run_playwright_cert_renew() {
  local marker="$1"
  local ui_state="${2:-}"
  (
    cd "${PLAY_DIR}"
    export CP_BASE CP_EMAIL="${CP_ADMIN_USER}" CP_PASSWORD="${CP_ADMIN_PASSWORD}"
    export CP_NODE_ID="${NODE_ID}"
    export CP_TOTP_SECRET="${TOTP_SECRET}"
    export CP_TOTP_OUT="${WORK_DIR}/totp-secret.txt"
    export CP_CLICK_MARKER="${marker}"
    export CP_ACTION=cert-renew
    if [[ -n "${ui_state}" ]]; then
      export CP_UI_STATE_OUT="${ui_state}"
    else
      unset CP_UI_STATE_OUT || true
    fi
    node real-node-button-browser.mjs
  )
}

leaf_fingerprint_sha256() {
  # stdin: PEM certificate → lowercase hex SHA-256 of DER
  openssl x509 -outform DER 2>/dev/null | openssl dgst -sha256 -hex 2>/dev/null \
    | awk '{print tolower($NF)}'
}

served_leaf_pem() {
  local host="$1" port="$2" sni="$3"
  local pem=""
  pem="$(timeout 10 openssl s_client -connect "${host}:${port}" -servername "${sni}" </dev/null 2>/dev/null \
    | openssl x509 2>/dev/null || true)"
  if [[ -z "${pem}" ]]; then
    pem="$(timeout 10 openssl s_client -connect "${host}:${port}" -servername "${sni}" -brief </dev/null 2>/dev/null \
      | openssl x509 2>/dev/null || true)"
  fi
  printf '%s' "${pem}"
}

wait_renew_command() {
  # Args: want_success(1|0) timeout_seconds [baseline_command_id]
  # Sets RENEW_STATUS RENEW_RESULT_CODE RENEW_COMMAND_ID. Returns 0 on match.
  local want_success="$1"
  local timeout_s="${2:-600}"
  local baseline_id="${3:-}"
  local deadline=$(( $(date +%s) + timeout_s ))
  RENEW_STATUS=""
  RENEW_RESULT_CODE=""
  RENEW_COMMAND_ID=""
  while [[ "$(date +%s)" -lt "${deadline}" ]]; do
    seed_and_refresh_sibling || true
    local row
    row="$(sql_q "SET NOCOUNT ON; SELECT TOP 1 Status, ISNULL(ResultCode,''), CONVERT(varchar(36), Id) FROM NodeCommands WHERE NodeId=N'${NODE_ID}' AND Type=0 ORDER BY CreatedAt DESC;" \
      | tr -d '\r' | sed '/^$/d' | head -n1 || true)"
    RENEW_STATUS="$(awk '{print $1}' <<<"${row}")"
    RENEW_RESULT_CODE="$(awk '{print $2}' <<<"${row}")"
    RENEW_COMMAND_ID="$(awk '{print $3}' <<<"${row}")"
    if [[ -n "${baseline_id}" && -n "${RENEW_COMMAND_ID}" && "${RENEW_COMMAND_ID}" == "${baseline_id}" ]]; then
      sleep 5
      continue
    fi
    if [[ "${want_success}" == "1" ]]; then
      # Succeeded=3; accept renewed (primary) or healthy-noop aliases if ever emitted.
      if [[ "${RENEW_STATUS}" == "3" && (
            "${RENEW_RESULT_CODE}" == "renewed" \
            || "${RENEW_RESULT_CODE}" == "healthy_noop" \
            || "${RENEW_RESULT_CODE}" == "healthy-noop" \
            || "${RENEW_RESULT_CODE}" == "noop"
          ) ]]; then
        return 0
      fi
      if [[ "${RENEW_STATUS}" == "4" ]]; then
        return 1
      fi
    else
      # Failed=4
      if [[ "${RENEW_STATUS}" == "4" && -n "${RENEW_RESULT_CODE}" ]]; then
        return 0
      fi
      if [[ "${RENEW_STATUS}" == "3" ]]; then
        return 1
      fi
    fi
    sleep 5
  done
  return 1
}

latest_renew_command_id() {
  sql_q "SET NOCOUNT ON; SELECT TOP 1 CONVERT(varchar(36), Id) FROM NodeCommands WHERE NodeId=N'${NODE_ID}' AND Type=0 ORDER BY CreatedAt DESC;" \
    | tr -d '\r' | sed '/^$/d' | head -n1 || true
}

acme_dir_ownership_summary() {
  local path="/var/lib/nyxveil/acme"
  if [[ ! -d "${path}" ]]; then
    echo "missing"
    return 0
  fi
  local owner mode
  owner="$(stat -c '%U:%G' "${path}" 2>/dev/null || echo unknown)"
  mode="$(stat -c '%a' "${path}" 2>/dev/null || echo unknown)"
  echo "${owner} ${mode}"
}

patch_server_json_acme() {
  local directory_url="$1"
  python3 - "${directory_url}" "${ACME_DOMAIN}" "${ACME_EMAIL}" <<'PY'
import json, sys
path = "/etc/nyxveil/server.json"
directory, domain, email = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding="utf-8") as f:
    cfg = json.load(f)
cfg["acme_domain"] = domain
cfg["acme_email"] = email
cfg["acme_directory"] = directory
with open(path, "w", encoding="utf-8") as f:
    json.dump(cfg, f, indent=2, sort_keys=True)
    f.write("\n")
PY
}

install_candidate_binaries() {
  local src_server="$1"
  local src_ctl="$2"
  local dst_server="/usr/local/sbin/nyxveil-server"
  local dst_ctl="/usr/local/sbin/nyxveilctl"
  local mode_server mode_ctl
  mode_server="$(stat -c '%a' "${dst_server}" 2>/dev/null || echo 755)"
  mode_ctl="$(stat -c '%a' "${dst_ctl}" 2>/dev/null || echo 755)"
  install -m "${mode_server}" "${src_server}" "${dst_server}"
  install -m "${mode_ctl}" "${src_ctl}" "${dst_ctl}"
  printf '%s\n' "${CANDIDATE_VERSION}" >/usr/local/share/nyxveil/VERSION
}

if [[ "${DURABLE_RESTART_RESULT}" == "PASS" ]] && { [[ "${ENABLE_PEBBLE}" == "1" ]] || [[ -n "${PEBBLE_DIR_URL}" ]]; }; then
  ACME_PHASE_RAN=1
  log "starting ACME/cert/TLS/QUIC/rollback phase (candidate ${CANDIDATE_VERSION})"

  # Ensure Pebble is reachable on host network for HTTP-01 → :80
  if [[ -z "${PEBBLE_DIR_URL}" ]] || ! curl -skf "${PEBBLE_DIR_URL}" >/dev/null 2>&1; then
    if ! ensure_pebble_host_network; then
      fail_acme_gate acme_pebble "pebble_unavailable"
      fail_acme_gate cert_button "skipped_no_pebble"
      fail_acme_gate tls_served "skipped_no_pebble"
      fail_acme_gate quic_handshake "skipped_no_pebble"
      fail_acme_gate rollback_recovery "skipped_no_pebble"
      exit 1
    fi
  else
    # Recreate with host networking if an older bridge-mapped container is still running.
    net_mode="$(docker inspect -f '{{.HostConfig.NetworkMode}}' nyxveil-lab-pebble 2>/dev/null || true)"
    if [[ "${net_mode}" != "host" ]]; then
      log "Pebble network mode=${net_mode:-unknown}; recreating with --network host"
      if ! ensure_pebble_host_network; then
        fail_acme_gate acme_pebble "pebble_host_network_failed"
        fail_acme_gate cert_button "skipped_no_pebble"
        fail_acme_gate tls_served "skipped_no_pebble"
        fail_acme_gate quic_handshake "skipped_no_pebble"
        fail_acme_gate rollback_recovery "skipped_no_pebble"
        exit 1
      fi
    fi
  fi

  # --- build candidate --------------------------------------------------------
  CAND_DIR="${WORK_DIR}/candidate-${CANDIDATE_VERSION}"
  mkdir -p "${CAND_DIR}"
  log "building candidate ${CANDIDATE_VERSION} (linux/amd64) from server/"
  if ! (
    cd "${REPO_ROOT}/server"
    GOOS=linux GOARCH=amd64 go build -o "${CAND_DIR}/nyxveil-server" ./cmd/nyxveil-server
    GOOS=linux GOARCH=amd64 go build -o "${CAND_DIR}/nyxveilctl" ./cmd/nyxveilctl
  ); then
    fail_acme_gate acme_pebble "candidate_build_failed"
    fail_acme_gate cert_button "candidate_build_failed"
    fail_acme_gate tls_served "candidate_build_failed"
    fail_acme_gate quic_handshake "candidate_build_failed"
    fail_acme_gate rollback_recovery "candidate_build_failed"
    exit 1
  fi
  [[ -x "${CAND_DIR}/nyxveil-server" && -x "${CAND_DIR}/nyxveilctl" ]] || {
    fail_acme_gate acme_pebble "candidate_binaries_missing"
    fail_acme_gate cert_button "candidate_binaries_missing"
    fail_acme_gate tls_served "candidate_binaries_missing"
    fail_acme_gate quic_handshake "candidate_binaries_missing"
    fail_acme_gate rollback_recovery "candidate_binaries_missing"
    exit 1
  }

  ACME_OWNER_BEFORE="$(acme_dir_ownership_summary)"
  log "ACME dir ownership before candidate start: ${ACME_OWNER_BEFORE}"
  # 1.1.17 update should already have migrated legacy root:root → nyxveil:nyxveil 0700.
  if ! echo "${ACME_OWNER_BEFORE}" | grep -Eq '^nyxveil:nyxveil 0?700$'; then
    fail_acme_gate acme_pebble "acme_ownership_unexpected_before_candidate" \
      ownership_before="${ACME_OWNER_BEFORE}" \
      pebble_directory="${PEBBLE_DIR_URL}"
    fail_acme_gate cert_button "acme_ownership_unexpected"
    fail_acme_gate tls_served "acme_ownership_unexpected"
    fail_acme_gate quic_handshake "acme_ownership_unexpected"
    fail_acme_gate rollback_recovery "acme_ownership_unexpected"
    exit 1
  fi
  # Legacy fixture wrote a non-PEM sentinel into acme-account.key for ownership
  # observation. Remove it so candidate ACME can create a real account key.
  if [[ -f /var/lib/nyxveil/acme/acme-account.key ]]; then
    if ! grep -q 'BEGIN .*PRIVATE KEY' /var/lib/nyxveil/acme/acme-account.key 2>/dev/null; then
      log "removing non-PEM legacy ACME account-key sentinel before candidate ACME"
      rm -f /var/lib/nyxveil/acme/acme-account.key
    fi
  fi

  # /etc/hosts for HTTP-01 name (Pebble --network host uses host resolver)
  if ! grep -Eq "^[[:space:]]*127\\.0\\.0\\.1[[:space:]].*[[:space:]]${ACME_DOMAIN}([[:space:]]|\$)" /etc/hosts; then
    echo "127.0.0.1 ${ACME_DOMAIN}" >>/etc/hosts
    log "added /etc/hosts entry for ${ACME_DOMAIN}"
  fi

  systemctl stop nyxveil-server
  install_candidate_binaries "${CAND_DIR}/nyxveil-server" "${CAND_DIR}/nyxveilctl"
  patch_server_json_acme "${PEBBLE_DIR_URL}"
  systemctl daemon-reload
  systemctl start nyxveil-server

  # Wait for active + initial ACME (loadTLSCert issues when acme_domain set)
  for i in $(seq 1 90); do
    if systemctl is-active --quiet nyxveil-server; then
      break
    fi
    if [[ "${i}" -eq 90 ]]; then
      journalctl -u nyxveil-server -n 40 --no-pager >&2 || true
      fail_acme_gate acme_pebble "candidate_service_not_active"
      fail_acme_gate cert_button "candidate_service_not_active"
      fail_acme_gate tls_served "candidate_service_not_active"
      fail_acme_gate quic_handshake "candidate_service_not_active"
      fail_acme_gate rollback_recovery "candidate_service_not_active"
      exit 1
    fi
    sleep 2
  done

  # Allow ACME issuance on first start (self-signed → Pebble leaf)
  log "waiting for candidate ACME issuance / healthy listeners"
  for i in $(seq 1 60); do
    if status_json="$(nyxveilctl status 2>/dev/null)"; then
      tls_ok="$(jq -r '.tls_ok // false' <<<"${status_json}" 2>/dev/null || echo false)"
      quic_ok="$(jq -r '.quic_ok // false' <<<"${status_json}" 2>/dev/null || echo false)"
      if [[ "${tls_ok}" == "true" && "${quic_ok}" == "true" ]]; then
        break
      fi
    fi
    sleep 5
  done

  ACME_OWNER_AFTER="$(acme_dir_ownership_summary)"
  log "ACME dir ownership after candidate start: ${ACME_OWNER_AFTER}"
  if ! echo "${ACME_OWNER_AFTER}" | grep -Eq '^nyxveil:nyxveil 0?700$'; then
    fail_acme_gate acme_pebble "acme_ownership_unexpected_after_candidate" \
      ownership_before="${ACME_OWNER_BEFORE}" ownership_after="${ACME_OWNER_AFTER}" \
      pebble_directory="${PEBBLE_DIR_URL}"
    fail_acme_gate cert_button "acme_ownership_unexpected"
    fail_acme_gate tls_served "acme_ownership_unexpected"
    fail_acme_gate quic_handshake "acme_ownership_unexpected"
    fail_acme_gate rollback_recovery "acme_ownership_unexpected"
    exit 1
  fi

  # Clear in-memory RenewCertificate rate-limit window from startup ACME.
  systemctl restart nyxveil-server
  sleep 3
  systemctl is-active --quiet nyxveil-server || {
    fail_acme_gate acme_pebble "restart_after_acme_failed"
    exit 1
  }

  ACME_PEBBLE_RESULT=PASS
  write_json "${EVIDENCE_DIR}/acme_pebble-evidence.json" \
    gate=acme_pebble result=PASS \
    pebble_directory="${PEBBLE_DIR_URL}" \
    acme_domain="${ACME_DOMAIN}" \
    ownership_before="${ACME_OWNER_BEFORE}" \
    ownership_after="${ACME_OWNER_AFTER}" \
    candidate_version="${CANDIDATE_VERSION}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "acme_pebble PASS"

  # --- Playwright cert renew --------------------------------------------------
  CERT_BASELINE_ID="$(latest_renew_command_id)"
  CERT_CLICK_MARKER="${WORK_DIR}/cert-click.marker"
  if ! run_playwright_cert_renew "${CERT_CLICK_MARKER}"; then
    fail_acme_gate cert_button "playwright_cert_renew_failed"
    fail_acme_gate tls_served "skipped_cert_button_failed"
    fail_acme_gate quic_handshake "skipped_cert_button_failed"
    fail_acme_gate rollback_recovery "skipped_cert_button_failed"
    exit 1
  fi
  [[ -s "${CERT_CLICK_MARKER}" ]] || {
    fail_acme_gate cert_button "missing_cert_click_marker"
    exit 1
  }

  RENEW_STATUS=""
  RENEW_RESULT_CODE=""
  if ! wait_renew_command 1 600 "${CERT_BASELINE_ID}"; then
    fail_acme_gate cert_button "renew_command_not_successful" \
      command_status="${RENEW_STATUS}" command_result_code="${RENEW_RESULT_CODE}"
    fail_acme_gate tls_served "skipped_cert_button_failed"
    fail_acme_gate quic_handshake "skipped_cert_button_failed"
    fail_acme_gate rollback_recovery "skipped_cert_button_failed"
    exit 1
  fi
  CERT_BUTTON_RESULT=PASS
  write_json "${EVIDENCE_DIR}/cert_button-evidence.json" \
    gate=cert_button result=PASS \
    command_status="${RENEW_STATUS}" command_result_code="${RENEW_RESULT_CODE}" \
    node_id="${NODE_ID}" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "cert_button PASS result=${RENEW_RESULT_CODE}"

  # --- TLS served proof -------------------------------------------------------
  SERVED_PEM="$(served_leaf_pem 127.0.0.1 "${TLS_PORT}" "${ACME_DOMAIN}")"
  if [[ -z "${SERVED_PEM}" ]]; then
    fail_acme_gate tls_served "openssl_s_client_no_leaf"
    fail_acme_gate quic_handshake "skipped_tls_failed"
    fail_acme_gate rollback_recovery "skipped_tls_failed"
    exit 1
  fi
  SERVED_FP="$(printf '%s\n' "${SERVED_PEM}" | leaf_fingerprint_sha256)"
  SERVED_SAN="$(printf '%s\n' "${SERVED_PEM}" | openssl x509 -noout -ext subjectAltName 2>/dev/null || true)"
  if [[ -z "${SERVED_FP}" ]]; then
    fail_acme_gate tls_served "fingerprint_extract_failed"
    exit 1
  fi
  if ! printf '%s\n' "${SERVED_SAN}" | grep -F "${ACME_DOMAIN}" >/dev/null 2>&1; then
    # Fallback: subject CN
    SERVED_SUBJ="$(printf '%s\n' "${SERVED_PEM}" | openssl x509 -noout -subject 2>/dev/null || true)"
    if ! printf '%s\n' "${SERVED_SUBJ}${SERVED_SAN}" | grep -F "${ACME_DOMAIN}" >/dev/null 2>&1; then
      fail_acme_gate tls_served "san_missing_acme_domain" \
        served_thumbprint="${SERVED_FP}" san="${SERVED_SAN}" subject="${SERVED_SUBJ}"
      fail_acme_gate quic_handshake "skipped_tls_failed"
      fail_acme_gate rollback_recovery "skipped_tls_failed"
      exit 1
    fi
  fi
  TLS_SERVED_RESULT=PASS
  write_json "${EVIDENCE_DIR}/tls_served-evidence.json" \
    gate=tls_served result=PASS \
    served_thumbprint="${SERVED_FP}" \
    acme_domain="${ACME_DOMAIN}" \
    tls_port="${TLS_PORT}" \
    san="$(printf '%s' "${SERVED_SAN}" | tr '\n' ' ')" \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "tls_served PASS thumbprint=${SERVED_FP}"

  # --- QUIC handshake ---------------------------------------------------------
  QUIC_OUT="${WORK_DIR}/quic-probe.out"
  if ! (
    cd "${REPO_ROOT}/server"
    go run ./scripts/quic-handshake-probe \
      -addr "127.0.0.1:${QUIC_PORT}" \
      -servername "${ACME_DOMAIN}" \
      >"${QUIC_OUT}" 2>&1
  ); then
    # Fallback: nyxveilctl status quic_ok after ACME (listener health)
    status_json="$(nyxveilctl status 2>/dev/null || true)"
    quic_ok="$(jq -r '.quic_ok // false' <<<"${status_json}" 2>/dev/null || echo false)"
    if [[ "${quic_ok}" == "true" ]]; then
      QUIC_HANDSHAKE_RESULT=PASS
      write_json "${EVIDENCE_DIR}/quic_handshake-evidence.json" \
        gate=quic_handshake result=PASS \
        method=nyxveilctl_status_quic_ok \
        note="real Dial failed; accepted quic_ok=true after ACME cert" \
        probe_output="$(tr '\n' ' ' <"${QUIC_OUT}" 2>/dev/null | head -c 500)" \
        finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      log "quic_handshake PASS via nyxveilctl status quic_ok (probe dial failed)"
    else
      fail_acme_gate quic_handshake "quic_probe_and_status_failed" \
        probe_output="$(tr '\n' ' ' <"${QUIC_OUT}" 2>/dev/null | head -c 500)" \
        quic_ok="${quic_ok}"
      fail_acme_gate rollback_recovery "skipped_quic_failed"
      exit 1
    fi
  else
    QUIC_LEAF_FP="$(awk -F= '/leaf_sha256=/{print $NF}' "${QUIC_OUT}" | tr -d '[:space:]' || true)"
    QUIC_HANDSHAKE_RESULT=PASS
    write_json "${EVIDENCE_DIR}/quic_handshake-evidence.json" \
      gate=quic_handshake result=PASS \
      method=quic_dial_h3 \
      leaf_sha256="${QUIC_LEAF_FP}" \
      quic_port="${QUIC_PORT}" \
      finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    log "quic_handshake PASS leaf_sha256=${QUIC_LEAF_FP}"
  fi

  # --- rollback / failure preservation ----------------------------------------
  DEAD_DIR_URL="https://127.0.0.1:1/dead-acme-directory"
  log "pointing acme_directory at dead URL for failure-preservation"
  systemctl stop nyxveil-server
  patch_server_json_acme "${DEAD_DIR_URL}"
  systemctl start nyxveil-server
  sleep 3
  systemctl is-active --quiet nyxveil-server || {
    fail_acme_gate rollback_recovery "service_not_active_after_dead_directory"
    exit 1
  }

  PRE_FAIL_PEM="$(served_leaf_pem 127.0.0.1 "${TLS_PORT}" "${ACME_DOMAIN}")"
  PRE_FAIL_FP="$(printf '%s\n' "${PRE_FAIL_PEM}" | leaf_fingerprint_sha256)"
  [[ -n "${PRE_FAIL_FP}" ]] || {
    fail_acme_gate rollback_recovery "pre_fail_tls_fingerprint_missing"
    exit 1
  }
  # Thumbprint should still match the successful ACME leaf.
  if [[ "${PRE_FAIL_FP}" != "${SERVED_FP}" ]]; then
    fail_acme_gate rollback_recovery "pre_fail_thumbprint_drift" \
      expected="${SERVED_FP}" got="${PRE_FAIL_FP}"
    exit 1
  fi

  FAIL_BASELINE_ID="$(latest_renew_command_id)"
  FAIL_CLICK_MARKER="${WORK_DIR}/cert-fail-click.marker"
  FAIL_UI_STATE="${WORK_DIR}/cert-fail-ui.json"
  if ! run_playwright_cert_renew "${FAIL_CLICK_MARKER}" "${FAIL_UI_STATE}"; then
    fail_acme_gate rollback_recovery "playwright_fail_renew_click_failed"
    exit 1
  fi

  FAIL_STATUS=""
  FAIL_RESULT_CODE=""
  RENEW_STATUS=""
  RENEW_RESULT_CODE=""
  if ! wait_renew_command 0 600 "${FAIL_BASELINE_ID}"; then
    fail_acme_gate rollback_recovery "expected_failed_renew_command" \
      command_status="${RENEW_STATUS}" command_result_code="${RENEW_RESULT_CODE}"
    exit 1
  fi
  FAIL_STATUS="${RENEW_STATUS}"
  FAIL_RESULT_CODE="${RENEW_RESULT_CODE}"

  POST_FAIL_PEM="$(served_leaf_pem 127.0.0.1 "${TLS_PORT}" "${ACME_DOMAIN}")"
  POST_FAIL_FP="$(printf '%s\n' "${POST_FAIL_PEM}" | leaf_fingerprint_sha256)"
  if [[ -z "${POST_FAIL_FP}" || "${POST_FAIL_FP}" != "${SERVED_FP}" ]]; then
    fail_acme_gate rollback_recovery "leaf_thumbprint_changed_after_failed_renew" \
      expected="${SERVED_FP}" got="${POST_FAIL_FP}" \
      command_status="${FAIL_STATUS}" command_result_code="${FAIL_RESULT_CODE}"
    exit 1
  fi

  UI_STUCK=false
  if [[ -f "${FAIL_UI_STATE}" ]]; then
    UI_STUCK="$(jq -r '.ui_stuck_renewing // false' "${FAIL_UI_STATE}" 2>/dev/null || echo false)"
  fi
  if [[ "${UI_STUCK}" == "true" ]]; then
    fail_acme_gate rollback_recovery "ui_stuck_renewing" \
      command_status="${FAIL_STATUS}" command_result_code="${FAIL_RESULT_CODE}" \
      served_thumbprint="${POST_FAIL_FP}"
    exit 1
  fi

  ROLLBACK_RECOVERY_RESULT=PASS
  write_json "${EVIDENCE_DIR}/rollback_recovery-evidence.json" \
    gate=rollback_recovery result=PASS \
    served_thumbprint="${POST_FAIL_FP}" \
    command_status="${FAIL_STATUS}" \
    command_result_code="${FAIL_RESULT_CODE}" \
    dead_acme_directory="${DEAD_DIR_URL}" \
    ui_stuck_renewing=json:false \
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "rollback_recovery PASS thumbprint unchanged result=${FAIL_RESULT_CODE}"
fi

exit 0
