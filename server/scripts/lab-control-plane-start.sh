#!/usr/bin/env bash
# Lab Control Plane for disposable Ubuntu node E2E (option C: build from repo source).
# Starts Microsoft SQL Server in Docker, migrates schema, HTTPS self-signed Kestrel,
# SuperAdmin via `dotnet ... admin create`, and seeds Location + BootstrapToken via SQL.
#
# Outputs a sourcable env file (--output-env) with CP_BASE, credentials, bootstrap token, CA PEM path.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LICENSING_ROOT="${REPO_ROOT}/licensing"

WORK_DIR=""
OUTPUT_ENV=""
CP_PORT="${NYXVEIL_LAB_CP_PORT:-18443}"
MSSQL_CONTAINER="${NYXVEIL_LAB_MSSQL_CONTAINER:-nyxveil-lab-mssql}"
MSSQL_IMAGE="${NYXVEIL_LAB_MSSQL_IMAGE:-mcr.microsoft.com/mssql/server:2022-latest}"
DB_NAME="${NYXVEIL_LAB_DB_NAME:-NyxveilControlPlane_LabNodeE2E}"
LOCATION_ID="${NYXVEIL_LAB_LOCATION_ID:-lab-e2e}"
ADMIN_USER="${NYXVEIL_LAB_ADMIN_USER:-lab-node-e2e@example.test}"
# DEV-ONLY placeholder KEK (matches appsettings.Development.json). Never reuse in production.
LICENSE_KEK_HEX="${NYXVEIL_LAB_LICENSE_KEK_HEX:-0000000000000000000000000000000000000000000000000000000000000001}"
SA_PASSWORD="${NYXVEIL_LAB_SA_PASSWORD:-}"
ADMIN_PASSWORD="${NYXVEIL_LAB_ADMIN_PASSWORD:-}"

usage() {
  cat <<'EOF'
Usage: lab-control-plane-start.sh --work-dir DIR --output-env FILE [options]

  --work-dir DIR       Scratch directory for certs, logs, PID, state
  --output-env FILE    Write KEY=VALUE exports for the caller to source
  --port N             HTTPS listen port (default 18443)
  -h, --help           Show help

Env overrides: NYXVEIL_LAB_* (port, container, image, db, location, admin, passwords, kek).
EOF
}

die() { echo "lab-control-plane-start: ERROR: $*" >&2; exit 1; }
log() { echo "lab-control-plane-start: $*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir) WORK_DIR="${2:-}"; shift 2 ;;
    --output-env) OUTPUT_ENV="${2:-}"; shift 2 ;;
    --port) CP_PORT="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "${WORK_DIR}" ]] || die "--work-dir required"
[[ -n "${OUTPUT_ENV}" ]] || die "--output-env required"
[[ -d "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web" ]] || die "Control Plane sources missing under ${LICENSING_ROOT}"

command -v docker >/dev/null 2>&1 || die "docker required"
command -v dotnet >/dev/null 2>&1 || die "dotnet required"
command -v openssl >/dev/null 2>&1 || die "openssl required"
command -v python3 >/dev/null 2>&1 || die "python3 required"
command -v curl >/dev/null 2>&1 || die "curl required"

mkdir -p "${WORK_DIR}"
WORK_DIR="$(cd "${WORK_DIR}" && pwd)"
STATE_DIR="${WORK_DIR}/cp-state"
CERT_DIR="${WORK_DIR}/certs"
LOG_DIR="${WORK_DIR}/logs"
KEYS_DIR="${WORK_DIR}/keys"
mkdir -p "${STATE_DIR}" "${CERT_DIR}" "${LOG_DIR}" "${KEYS_DIR}"

if [[ -z "${SA_PASSWORD}" ]]; then
  SA_PASSWORD="Nyxveil-Sql-$(openssl rand -hex 8)Aa1!"
fi
if [[ -z "${ADMIN_PASSWORD}" ]]; then
  ADMIN_PASSWORD="Nyxveil-E2E-$(openssl rand -hex 6)Aa1!"
fi

# --- SQL Server (Docker) -------------------------------------------------------
start_mssql() {
  if docker ps --format '{{.Names}}' | grep -qx "${MSSQL_CONTAINER}"; then
    log "reusing running MSSQL container ${MSSQL_CONTAINER}"
    return 0
  fi
  if docker ps -a --format '{{.Names}}' | grep -qx "${MSSQL_CONTAINER}"; then
    log "starting existing MSSQL container ${MSSQL_CONTAINER}"
    docker start "${MSSQL_CONTAINER}" >/dev/null
  else
    log "pulling/starting MSSQL ${MSSQL_IMAGE}"
    docker run -d --name "${MSSQL_CONTAINER}" \
      -e ACCEPT_EULA=Y \
      -e "MSSQL_SA_PASSWORD=${SA_PASSWORD}" \
      -p 1433:1433 \
      "${MSSQL_IMAGE}" >/dev/null
  fi

  local i
  for i in $(seq 1 60); do
    if docker exec "${MSSQL_CONTAINER}" \
      /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P "${SA_PASSWORD}" -C -Q "SELECT 1" \
      >/dev/null 2>&1; then
      log "MSSQL ready"
      return 0
    fi
    # tools18 path may differ on older images
    if docker exec "${MSSQL_CONTAINER}" \
      /opt/mssql-tools/bin/sqlcmd -S localhost -U sa -P "${SA_PASSWORD}" -C -Q "SELECT 1" \
      >/dev/null 2>&1; then
      log "MSSQL ready"
      return 0
    fi
    sleep 3
  done
  die "MSSQL did not become ready"
}

sqlcmd_exec() {
  local db="${1:-master}"
  shift
  if docker exec -i "${MSSQL_CONTAINER}" test -x /opt/mssql-tools18/bin/sqlcmd; then
    docker exec -i "${MSSQL_CONTAINER}" /opt/mssql-tools18/bin/sqlcmd \
      -S localhost -U sa -P "${SA_PASSWORD}" -C -d "${db}" "$@"
  else
    docker exec -i "${MSSQL_CONTAINER}" /opt/mssql-tools/bin/sqlcmd \
      -S localhost -U sa -P "${SA_PASSWORD}" -C -d "${db}" "$@"
  fi
}

start_mssql

# Persist SA password for this workdir so restarts can reconnect.
printf '%s\n' "${SA_PASSWORD}" >"${STATE_DIR}/sa.password"
chmod 600 "${STATE_DIR}/sa.password"

CS="Server=127.0.0.1,1433;Database=${DB_NAME};User Id=sa;Password=${SA_PASSWORD};TrustServerCertificate=True;Encrypt=True;MultipleActiveResultSets=true"

# CA PEM is captured from the live SelfSigned leaf after CP starts.
CA_PEM="${CERT_DIR}/lab-cp-ca.pem"
mkdir -p "${CERT_DIR}"

# --- Build + migrate ------------------------------------------------------------
log "building Control Plane Web (Release)"
dotnet build "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/Nyxveil.ControlPlane.Web.csproj" -c Release --nologo -v q

export ConnectionStrings__ControlPlane="${CS}"
export Database__TrustSqlServerCertificate=true
export Database__Encrypt=true
export ASPNETCORE_ENVIRONMENT=Development
export DOTNET_ENVIRONMENT=Development
export Security__LicenseKekHex="${LICENSE_KEK_HEX}"
export Https__RequireHttpsInProduction=false
# Ephemeral SelfSigned for lab; capture served leaf PEM for node --control-plane-ca-file.
export Certificate__Mode=SelfSigned
export Certificate__ValidationMode=SelfSignedPinned
export Hosting__BindAddress=127.0.0.1
export Hosting__Port="${CP_PORT}"
export Hosting__PublicHostname=127.0.0.1
export Hosting__PublicBaseUrl="https://127.0.0.1:${CP_PORT}"
export Signing__KeyProtectionPath="${KEYS_DIR}"
export Logging__File__Enabled=true
export Logging__File__Directory="${LOG_DIR}"
export ServerReleasePolicy__CacheMinutes=1
export ServerReleasePolicy__GitHubOwner=Moroz1212
export ServerReleasePolicy__GitHubRepo=Nyxveil
export UI__ShowTestNodes=true
# Lab TTL acceleration: DeliveryTtl must be shorter than artificial update delay.
export NYXVEIL_NODE_COMMAND_DELIVERY_TTL_SECONDS="${NYXVEIL_NODE_COMMAND_DELIVERY_TTL_SECONDS:-15}"
export NYXVEIL_NODE_COMMAND_PROGRESS_LEASE_SECONDS="${NYXVEIL_NODE_COMMAND_PROGRESS_LEASE_SECONDS:-120}"
export NYXVEIL_NODE_COMMAND_UPDATE_EXECUTION_TIMEOUT_SECONDS="${NYXVEIL_NODE_COMMAND_UPDATE_EXECUTION_TIMEOUT_SECONDS:-1800}"
export NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS="${NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS:-25}"

log "applying EF migrations"
if ! dotnet tool list -g 2>/dev/null | grep -q 'dotnet-ef'; then
  dotnet tool install -g dotnet-ef --version 10.0.* >/dev/null 2>&1 || \
    dotnet tool update -g dotnet-ef --version 10.0.* >/dev/null 2>&1 || true
fi
export PATH="${PATH}:${HOME}/.dotnet/tools"
dotnet ef database update \
  --project "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Infrastructure/Nyxveil.ControlPlane.Infrastructure.csproj" \
  --startup-project "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/Nyxveil.ControlPlane.Web.csproj" \
  --configuration Release \
  --no-build 2>/dev/null \
  || dotnet ef database update \
  --project "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Infrastructure/Nyxveil.ControlPlane.Infrastructure.csproj" \
  --startup-project "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/Nyxveil.ControlPlane.Web.csproj" \
  --configuration Release

# --- SuperAdmin -----------------------------------------------------------------
WEB_DLL="${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/bin/Release/net10.0/Nyxveil.ControlPlane.Web.dll"
[[ -f "${WEB_DLL}" ]] || WEB_DLL="$(find "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/bin/Release" -name 'Nyxveil.ControlPlane.Web.dll' | head -n1)"
[[ -f "${WEB_DLL}" ]] || die "Web DLL not found after build"

log "creating SuperAdmin ${ADMIN_USER}"
set +e
printf '%s\n' "${ADMIN_PASSWORD}" | \
  NYXVEIL_ADMIN_PASSWORD="${ADMIN_PASSWORD}" \
  ConnectionStrings__ControlPlane="${CS}" \
  Security__LicenseKekHex="${LICENSE_KEK_HEX}" \
  Https__RequireHttpsInProduction=false \
  ASPNETCORE_ENVIRONMENT=Development \
  dotnet "${WEB_DLL}" admin create --username "${ADMIN_USER}"
admin_rc=$?
set -e
if [[ "${admin_rc}" -ne 0 && "${admin_rc}" -ne 2 ]]; then
  die "admin create failed exit=${admin_rc}"
fi

# --- Seed Location + BootstrapToken (SQL) --------------------------------------
BOOTSTRAP_RAW="$(python3 - <<'PY'
import secrets
print("nvp_boot_" + secrets.token_hex(16))
PY
)"
BOOTSTRAP_ID="$(python3 - <<'PY'
import uuid
print(str(uuid.uuid4()))
PY
)"
VERIFIER="$(LICENSE_KEK_HEX="${LICENSE_KEK_HEX}" BOOTSTRAP_RAW="${BOOTSTRAP_RAW}" python3 - <<'PY'
import hashlib, hmac, os
kek = bytes.fromhex(os.environ["LICENSE_KEK_HEX"])
raw = os.environ["BOOTSTRAP_RAW"].encode("utf-8")
print("hmac1:" + hmac.new(kek, raw, hashlib.sha256).hexdigest())
PY
)"
EXPIRES="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(days=2)).strftime("%Y-%m-%d %H:%M:%S"))
PY
)"
NOW_UTC="$(python3 - <<'PY'
from datetime import datetime, timezone
print(datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S"))
PY
)"

log "seeding Location=${LOCATION_ID} and BootstrapToken via SQL"
sqlcmd_exec "${DB_NAME}" -Q "
IF NOT EXISTS (SELECT 1 FROM Locations WHERE LocationId=N'${LOCATION_ID}')
BEGIN
  INSERT INTO Locations (LocationId, Code, Country, City, DisplayName, Enabled, SortOrder, CreatedAt, UpdatedAt, CountryCode)
  VALUES (N'${LOCATION_ID}', N'${LOCATION_ID}', N'Lab', N'E2E', N'Lab E2E Location', 1, 0, '${NOW_UTC}', '${NOW_UTC}', N'XX');
END
IF NOT EXISTS (SELECT 1 FROM BootstrapTokens WHERE BootstrapId='${BOOTSTRAP_ID}')
BEGIN
  INSERT INTO BootstrapTokens (BootstrapId, Verifier, ExpiresAt, MaxUses, UsedCount, AllowedLocation, Status, CreatedAt, CreatedBy, Note)
  VALUES ('${BOOTSTRAP_ID}', N'${VERIFIER}', '${EXPIRES}', 5, 0, N'${LOCATION_ID}', 0, '${NOW_UTC}', N'lab-e2e', N'lab node e2e');
END
" >/dev/null

# --- Start Web ------------------------------------------------------------------
PID_FILE="${STATE_DIR}/cp.pid"
if [[ -f "${PID_FILE}" ]] && kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
  log "stopping previous lab CP pid=$(cat "${PID_FILE}")"
  kill "$(cat "${PID_FILE}")" 2>/dev/null || true
  sleep 2
fi

log "starting Control Plane Web on https://127.0.0.1:${CP_PORT}"
WEB_PROJ="${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web/Nyxveil.ControlPlane.Web.csproj"
(
  cd "${LICENSING_ROOT}/src/Nyxveil.ControlPlane.Web"
  nohup env \
    ConnectionStrings__ControlPlane="${CS}" \
    Database__TrustSqlServerCertificate=true \
    Database__Encrypt=true \
    ASPNETCORE_ENVIRONMENT=Development \
    DOTNET_ENVIRONMENT=Development \
    Security__LicenseKekHex="${LICENSE_KEK_HEX}" \
    Https__RequireHttpsInProduction=false \
    Certificate__Mode=SelfSigned \
    Certificate__ValidationMode=SelfSignedPinned \
    Hosting__BindAddress=127.0.0.1 \
    Hosting__Port="${CP_PORT}" \
    Hosting__PublicHostname=127.0.0.1 \
    Hosting__PublicBaseUrl="https://127.0.0.1:${CP_PORT}" \
    Signing__KeyProtectionPath="${KEYS_DIR}" \
    Logging__File__Enabled=true \
    Logging__File__Directory="${LOG_DIR}" \
    ServerReleasePolicy__CacheMinutes=1 \
    ServerReleasePolicy__GitHubOwner=Moroz1212 \
    ServerReleasePolicy__GitHubRepo=Nyxveil \
    UI__ShowTestNodes=true \
    NYXVEIL_NODE_COMMAND_DELIVERY_TTL_SECONDS="${NYXVEIL_NODE_COMMAND_DELIVERY_TTL_SECONDS:-15}" \
    NYXVEIL_NODE_COMMAND_PROGRESS_LEASE_SECONDS="${NYXVEIL_NODE_COMMAND_PROGRESS_LEASE_SECONDS:-120}" \
    NYXVEIL_NODE_COMMAND_UPDATE_EXECUTION_TIMEOUT_SECONDS="${NYXVEIL_NODE_COMMAND_UPDATE_EXECUTION_TIMEOUT_SECONDS:-1800}" \
    NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS="${NYXVEIL_UPDATE_ARTIFICIAL_DELAY_SECONDS:-25}" \
    ASPNETCORE_URLS="" \
    dotnet run --project "${WEB_PROJ}" -c Release --no-build --no-launch-profile \
    >"${LOG_DIR}/cp-web.stdout" 2>"${LOG_DIR}/cp-web.stderr" &
  echo $! >"${PID_FILE}"
)

CP_BASE="https://127.0.0.1:${CP_PORT}"
for i in $(seq 1 60); do
  if curl -fsSk --connect-timeout 2 --max-time 5 "${CP_BASE}/health/live" >/dev/null 2>&1; then
    log "Control Plane live"
    break
  fi
  if [[ "${i}" -eq 60 ]]; then
    tail -n 80 "${LOG_DIR}/cp-web.stderr" >&2 || true
    die "Control Plane did not become live at ${CP_BASE}/health/live"
  fi
  sleep 2
done

# Capture currently served leaf as CA pin for node install.
echo | openssl s_client -connect "127.0.0.1:${CP_PORT}" -servername 127.0.0.1 2>/dev/null \
  | openssl x509 -outform PEM >"${CA_PEM}"
[[ -s "${CA_PEM}" ]] || die "failed to capture lab CP certificate PEM"

# Write sourcable env
umask 077
cat >"${OUTPUT_ENV}" <<EOF
CP_BASE=${CP_BASE}
CP_PORT=${CP_PORT}
CP_CA_PEM=${CA_PEM}
CP_ADMIN_USER=${ADMIN_USER}
CP_ADMIN_PASSWORD=${ADMIN_PASSWORD}
CP_LOCATION_ID=${LOCATION_ID}
CP_BOOTSTRAP_TOKEN=${BOOTSTRAP_RAW}
CP_LICENSE_KEK_HEX=${LICENSE_KEK_HEX}
CP_SQL_CS=${CS}
CP_MSSQL_CONTAINER=${MSSQL_CONTAINER}
CP_DB_NAME=${DB_NAME}
CP_SA_PASSWORD=${SA_PASSWORD}
CP_PID_FILE=${PID_FILE}
CP_WORK_DIR=${WORK_DIR}
CP_WEB_DLL=${WEB_DLL}
EOF
chmod 600 "${OUTPUT_ENV}"

log "wrote ${OUTPUT_ENV}"
log "LAB_CONTROL_PLANE=READY base=${CP_BASE}"
exit 0
