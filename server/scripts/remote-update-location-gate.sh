#!/usr/bin/env bash
# Staging-only dual-node UpdateNodeLatest location-safety gate.
# Proves last-healthy / concurrent / same-location sibling rules via SuperAdmin JSON API.
#
# Auth: Identity cookie login against POST /account/login (form fields email + password).
# Password NEVER on argv — use env/file/stdin.
#
# Admin API (cookie jar):
#   POST /api/v1/admin/nodes/{nodeId}/commands   {"type":"UpdateNodeLatest"}
#   POST /api/v1/admin/nodes/{nodeId}/draining   {"draining":true|false}
#   GET  /api/v1/admin/nodes/{nodeId}
set -euo pipefail
umask 077

REPORT=/tmp/nyxveil-remote-update-gate-report.txt
: >"${REPORT}"

PASS_N=0
FAIL_N=0
SKIP_N=0
NOT_EXECUTED_N=0
CONFIRM_STAGING=0

CP_URL="${CP_URL:-}"
LOCATION_ID="${LOCATION_ID:-}"
NODE_A_ID="${NODE_A_ID:-}"
NODE_B_ID="${NODE_B_ID:-}"
TARGET_VERSION="${TARGET_VERSION:-}"
CROSS_LOCATION_NODE_ID="${CROSS_LOCATION_NODE_ID:-}"
ADMIN_EMAIL="${ADMIN_EMAIL:-${NYXVEIL_ADMIN_EMAIL:-}}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-${NYXVEIL_ADMIN_PASSWORD:-}}"
ADMIN_PASSWORD_FILE="${ADMIN_PASSWORD_FILE:-${NYXVEIL_ADMIN_PASSWORD_FILE:-}}"

COOKIE_JAR=""
WORK=""

usage() {
  cat <<EOF
Usage: $0 --confirm-staging --cp-url URL --location-id ID \\
  --node-a-id ID --node-b-id ID --target-version X.Y.Z [options]

Required:
  --confirm-staging
  --cp-url URL                 (or CP_URL)
  --location-id ID             (or LOCATION_ID)
  --node-a-id ID               (or NODE_A_ID)
  --node-b-id ID               (or NODE_B_ID)
  --target-version X.Y.Z       (or TARGET_VERSION)

Admin auth (NOT argv password):
  --admin-email EMAIL          (or ADMIN_EMAIL / NYXVEIL_ADMIN_EMAIL)
  --admin-password-file PATH   (or ADMIN_PASSWORD_FILE)
  OR ADMIN_PASSWORD / NYXVEIL_ADMIN_PASSWORD env
  OR password on stdin (one line) when not a tty

Optional:
  --cross-location-node-id ID  prove sibling scope (SKIP if omitted)
  --poll-seconds N             default 15
  --update-timeout-seconds N   default 900

Report: ${REPORT}
Exit 0 only when all required scenarios PASS.
EOF
}

log() { printf '%s\n' "$*" | tee -a "${REPORT}"; }
record() {
  local name="$1" status="$2" detail="${3:-}"
  log "${name}=${status} ${detail}"
  case "${status}" in
    PASS) PASS_N=$((PASS_N + 1)) ;;
    FAIL) FAIL_N=$((FAIL_N + 1)) ;;
    SKIP) SKIP_N=$((SKIP_N + 1)) ;;
    NOT_EXECUTED) NOT_EXECUTED_N=$((NOT_EXECUTED_N + 1)) ;;
    *) FAIL_N=$((FAIL_N + 1)) ;;
  esac
}

die() { log "ERROR: $*"; exit 2; }

cleanup() {
  ADMIN_PASSWORD=""
  unset ADMIN_PASSWORD || true
  if [[ -n "${WORK}" && -d "${WORK}" ]]; then
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

POLL_SECONDS=15
UPDATE_TIMEOUT=900

while [[ $# -gt 0 ]]; do
  case "$1" in
    --confirm-staging) CONFIRM_STAGING=1; shift ;;
    --cp-url) CP_URL="${2:-}"; shift 2 ;;
    --location-id) LOCATION_ID="${2:-}"; shift 2 ;;
    --node-a-id) NODE_A_ID="${2:-}"; shift 2 ;;
    --node-b-id) NODE_B_ID="${2:-}"; shift 2 ;;
    --target-version) TARGET_VERSION="${2:-}"; shift 2 ;;
    --cross-location-node-id) CROSS_LOCATION_NODE_ID="${2:-}"; shift 2 ;;
    --admin-email) ADMIN_EMAIL="${2:-}"; shift 2 ;;
    --admin-password-file) ADMIN_PASSWORD_FILE="${2:-}"; shift 2 ;;
    --admin-password)
      echo "ERROR: --admin-password forbidden (use file/env/stdin)" >&2
      exit 2
      ;;
    --poll-seconds) POLL_SECONDS="${2:-}"; shift 2 ;;
    --update-timeout-seconds) UPDATE_TIMEOUT="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

log "=== NYXVEIL REMOTE UPDATE LOCATION GATE ==="
log "started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ "${CONFIRM_STAGING}" -ne 1 ]]; then
  record CONFIRM_STAGING FAIL "missing --confirm-staging"
  exit 1
fi
record CONFIRM_STAGING PASS

[[ -n "${CP_URL}" ]] || die "missing --cp-url / CP_URL"
[[ -n "${LOCATION_ID}" ]] || die "missing --location-id / LOCATION_ID"
[[ -n "${NODE_A_ID}" ]] || die "missing --node-a-id / NODE_A_ID"
[[ -n "${NODE_B_ID}" ]] || die "missing --node-b-id / NODE_B_ID"
[[ -n "${TARGET_VERSION}" ]] || die "missing --target-version / TARGET_VERSION"
[[ -n "${ADMIN_EMAIL}" ]] || die "missing --admin-email / ADMIN_EMAIL"
[[ "${NODE_A_ID}" != "${NODE_B_ID}" ]] || die "NODE_A_ID and NODE_B_ID must differ"

CP_URL="${CP_URL%/}"
need_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
need_cmd curl
if ! command -v jq >/dev/null 2>&1 && ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
  die "jq or python required"
fi

WORK="$(mktemp -d /tmp/nyxveil-remote-update-gate.XXXXXX)"
COOKIE_JAR="${WORK}/cookies.txt"
HDR="${WORK}/headers.txt"

# --- password (never log) ---
set +x
{ set +o xtrace; } 2>/dev/null || true
if [[ -z "${ADMIN_PASSWORD}" && -n "${ADMIN_PASSWORD_FILE}" ]]; then
  [[ -f "${ADMIN_PASSWORD_FILE}" ]] || die "admin password file missing"
  ADMIN_PASSWORD="$(tr -d '\r\n' < "${ADMIN_PASSWORD_FILE}")"
fi
if [[ -z "${ADMIN_PASSWORD}" && ! -t 0 ]]; then
  ADMIN_PASSWORD="$(head -n 1 | tr -d '\r\n' || true)"
fi
[[ -n "${ADMIN_PASSWORD}" ]] || die "admin password required via env/file/stdin"
record ADMIN_AUTH_SOURCE PASS "password acquired (not logged)"

json_get() {
  local file="$1" expr="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r "${expr}" "${file}" 2>/dev/null || true
    return
  fi
  local py=python3
  command -v python3 >/dev/null 2>&1 || py=python
  "${py}" - "${file}" "${expr}" <<'PY' 2>/dev/null || true
import json, sys
path, expr = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    d = json.load(f)
# tiny subset of jq paths used by this harness
def walk(obj, parts):
    cur = obj
    for p in parts:
        if p == "":
            continue
        if isinstance(cur, dict):
            cur = cur.get(p)
        else:
            return None
    return cu
parts = [p for p in expr.lstrip(".").replace("//", "/").split(".") if p and p != "//empty"]
# strip jq suffixes like // empty
clean = []
for p in expr.split("."):
    p = p.strip()
    if not p or p == "//empty":
        continue
    if "//" in p:
        p = p.split("//", 1)[0]
    if p.startswith('"') and p.endswith('"'):
        p = p[1:-1]
    clean.append(p)
cur = d
for p in clean:
    if isinstance(cur, dict) and p in cur:
        cur = cur[p]
    else:
        cur = ""
        break
if cur is None:
    cur = ""
print(cur if not isinstance(cur, (dict, list)) else json.dumps(cur))
PY
}

http_code_body() {
  # usage: http_code_body METHOD URL [json-body-file]
  local method="$1" url="$2" body="${3:-}"
  local out="${WORK}/resp.body" code
  : >"${HDR}"
  if [[ -n "${body}" ]]; then
    code="$(curl -sS -o "${out}" -D "${HDR}" -w '%{http_code}' \
      -X "${method}" "${url}" \
      -H 'Content-Type: application/json' \
      -H 'Accept: application/json' \
      -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" \
      --connect-timeout 15 --max-time 60 \
      --data-binary @"${body}")"
  else
    code="$(curl -sS -o "${out}" -D "${HDR}" -w '%{http_code}' \
      -X "${method}" "${url}" \
      -H 'Accept: application/json' \
      -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" \
      --connect-timeout 15 --max-time 60)"
  fi
  printf '%s' "${code}"
}

# --- Identity cookie login (Login.razor: email + password) ---
set +x
{ set +o xtrace; } 2>/dev/null || true
LOGIN_CODE="$(curl -sS -o "${WORK}/login.body" -D "${WORK}/login.hdr" -w '%{http_code}' \
  -X POST "${CP_URL}/account/login" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" \
  --connect-timeout 15 --max-time 60 \
  --data-urlencode "email=${ADMIN_EMAIL}" \
  --data-urlencode "password=${ADMIN_PASSWORD}" \
  --data-urlencode "returnUrl=/")"
ADMIN_PASSWORD=""
unset ADMIN_PASSWORD || true

if [[ "${LOGIN_CODE}" != "302" && "${LOGIN_CODE}" != "200" ]]; then
  record LOGIN FAIL "http=${LOGIN_CODE}"
  exit 1
fi
if grep -qi 'error=1' "${WORK}/login.hdr" 2>/dev/null; then
  record LOGIN FAIL "redirect indicates auth error"
  exit 1
fi
if ! grep -qi 'Nyxveil.ControlPlane.Auth' "${COOKIE_JAR}" 2>/dev/null; then
  # Some hosts may set cookie with different casing/path; still try API.
  log "WARN: auth cookie name not found in jar; continuing to API probe"
fi
record LOGIN PASS "http=${LOGIN_CODE}"

get_node() {
  local id="$1" out="$2"
  local code
  code="$(http_code_body GET "${CP_URL}/api/v1/admin/nodes/${id}")"
  cp -f "${WORK}/resp.body" "${out}" 2>/dev/null || true
  printf '%s' "${code}"
}

set_draining() {
  local id="$1" draining="$2"
  printf '{"draining":%s}\n' "${draining}" >"${WORK}/drain.json"
  http_code_body POST "${CP_URL}/api/v1/admin/nodes/${id}/draining" "${WORK}/drain.json"
}

enqueue_update() {
  local id="$1"
  printf '{"type":"UpdateNodeLatest"}\n' >"${WORK}/cmd.json"
  http_code_body POST "${CP_URL}/api/v1/admin/nodes/${id}/commands" "${WORK}/cmd.json"
}

node_field() {
  local file="$1" field="$2"
  json_get "${file}" ".${field}"
}

is_truthy() {
  case "${1:-}" in
    true|True|TRUE|1|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

node_is_healthy_online() {
  local file="$1"
  local status enabled draining maintenance version location
  status="$(node_field "${file}" status)"
  [[ -z "${status}" ]] && status="$(node_field "${file}" runtimeStatus)"
  [[ -z "${status}" ]] && status="$(node_field "${file}" health)"
  enabled="$(node_field "${file}" enabled)"
  draining="$(node_field "${file}" draining)"
  maintenance="$(node_field "${file}" maintenanceMode)"
  [[ -z "${maintenance}" ]] && maintenance="$(node_field "${file}" maintenance)"
  version="$(node_field "${file}" serverVersion)"
  [[ -z "${version}" ]] && version="$(node_field "${file}" version)"
  location="$(node_field "${file}" locationId)"

  # Accept common healthy markers
  case "${status,,}" in
    healthy|online|ok|active) ;;
    *)
      # nested health object?
      if ! is_truthy "$(node_field "${file}" healthy)"; then
        return 1
      fi
      ;;
  esac
  if [[ -n "${enabled}" ]] && ! is_truthy "${enabled}"; then
    return 1
  fi
  if is_truthy "${draining}"; then
    return 1
  fi
  if is_truthy "${maintenance}"; then
    return 1
  fi
  return 0
}

wait_node_version() {
  local id="$1" want="$2" deadline=$((SECONDS + UPDATE_TIMEOUT))
  local code file ver status
  file="${WORK}/wait-${id}.json"
  while (( SECONDS < deadline )); do
    code="$(get_node "${id}" "${file}")"
    ver="$(node_field "${file}" serverVersion)"
    [[ -z "${ver}" ]] && ver="$(node_field "${file}" version)"
    status="$(node_field "${file}" lastCommandStatus)"
    [[ -z "${status}" ]] && status="$(node_field "${file}" commandStatus)"
    if [[ "${ver}" == "${want}" ]]; then
      echo "version=${ver}"
      return 0
    fi
    # command succeeded field if present
    if [[ "$(node_field "${file}" lastCommandSuccess)" == "true" && "${ver}" == "${want}" ]]; then
      echo "version=${ver}"
      return 0
    fi
    sleep "${POLL_SECONDS}"
  done
  echo "timeout version=$(node_field "${file}" serverVersion) http_last=${code:-}"
  return 1
}

# Snapshot pre-update state for STATE RESTORE
A_BEFORE="${WORK}/a-before.json"
B_BEFORE="${WORK}/b-before.json"
code_a="$(get_node "${NODE_A_ID}" "${A_BEFORE}")"
code_b="$(get_node "${NODE_B_ID}" "${B_BEFORE}")"
if [[ "${code_a}" != "200" || "${code_b}" != "200" ]]; then
  record PREFLIGHT_NODES FAIL "GET node A http=${code_a} B http=${code_b}"
  exit 1
fi
record PREFLIGHT_NODES PASS "fetched A and B"

A_ENABLED_BEFORE="$(node_field "${A_BEFORE}" enabled)"
A_DRAINING_BEFORE="$(node_field "${A_BEFORE}" draining)"
A_MAINT_BEFORE="$(node_field "${A_BEFORE}" maintenanceMode)"
[[ -z "${A_MAINT_BEFORE}" ]] && A_MAINT_BEFORE="$(node_field "${A_BEFORE}" maintenance)"
A_LOC="$(node_field "${A_BEFORE}" locationId)"
B_LOC="$(node_field "${B_BEFORE}" locationId)"
if [[ -n "${A_LOC}" && -n "${B_LOC}" && "${A_LOC}" != "${LOCATION_ID}" ]]; then
  log "WARN: node A locationId=${A_LOC} differs from --location-id=${LOCATION_ID}"
fi
if [[ -n "${A_LOC}" && -n "${B_LOC}" && "${A_LOC}" != "${B_LOC}" ]]; then
  record SAME_LOCATION_PRECHECK FAIL "A loc=${A_LOC} B loc=${B_LOC}"
  exit 1
fi
record SAME_LOCATION_PRECHECK PASS "A/B same location"

# =============================================================================
# 1) LAST HEALTHY: drain B, update A → BLOCK/CONFLICT, restore B
# =============================================================================
log "--- scenario LAST_HEALTHY ---"
drain_code="$(set_draining "${NODE_B_ID}" true)"
if [[ "${drain_code}" != "200" && "${drain_code}" != "204" ]]; then
  record LAST_HEALTHY FAIL "drain B http=${drain_code}"
else
  sleep 2
  upd_code="$(enqueue_update "${NODE_A_ID}")"
  cp -f "${WORK}/resp.body" "${WORK}/last-healthy-resp.json" || true
  if [[ "${upd_code}" == "409" ]]; then
    record LAST_HEALTHY PASS "update A blocked http=${upd_code}"
  elif [[ "${upd_code}" == "400" || "${upd_code}" == "422" ]] && grep -qiE 'conflict|last eligible|last healthy|refusing disruptive' "${WORK}/last-healthy-resp.json" 2>/dev/null; then
    record LAST_HEALTHY PASS "update A blocked http=${upd_code}"
  elif grep -qiE 'conflict|last eligible|last healthy|refusing disruptive' "${WORK}/last-healthy-resp.json" 2>/dev/null; then
    record LAST_HEALTHY PASS "update A blocked http=${upd_code} (conflict body)"
  else
    record LAST_HEALTHY FAIL "expected BLOCK/CONFLICT got http=${upd_code} body=$(head -c 200 "${WORK}/last-healthy-resp.json" 2>/dev/null || true)"
  fi
  restore_code="$(set_draining "${NODE_B_ID}" false)"
  if [[ "${restore_code}" == "200" || "${restore_code}" == "204" ]]; then
    record LAST_HEALTHY_RESTORE_B PASS "draining=false http=${restore_code}"
  else
    record LAST_HEALTHY_RESTORE_B FAIL "restore B draining http=${restore_code}"
  fi
fi

# Ensure B accepting again before continuing
sleep 3
get_node "${NODE_B_ID}" "${WORK}/b-after-restore.json" >/dev/null || true

# =============================================================================
# 2) TWO HEALTHY: enqueue update A; poll B stays healthy; wait A reaches target
# =============================================================================
log "--- scenario TWO_HEALTHY ---"
get_node "${NODE_A_ID}" "${WORK}/a-two.json" >/dev/null
get_node "${NODE_B_ID}" "${WORK}/b-two.json" >/dev/null
if ! node_is_healthy_online "${WORK}/a-two.json" || ! node_is_healthy_online "${WORK}/b-two.json"; then
  record TWO_HEALTHY FAIL "both nodes must be healthy/online/accepting before update"
else
  upd_code="$(enqueue_update "${NODE_A_ID}")"
  cp -f "${WORK}/resp.body" "${WORK}/two-healthy-enqueue.json" || true
  if [[ "${upd_code}" != "200" && "${upd_code}" != "201" && "${upd_code}" != "202" ]]; then
    record TWO_HEALTHY FAIL "enqueue A http=${upd_code} body=$(head -c 200 "${WORK}/two-healthy-enqueue.json" || true)"
  else
    record TWO_HEALTHY_ENQUEUE PASS "http=${upd_code}"
    B_OK=1
    deadline=$((SECONDS + UPDATE_TIMEOUT))
    while (( SECONDS < deadline )); do
      get_node "${NODE_B_ID}" "${WORK}/b-poll.json" >/dev/null || true
      if ! node_is_healthy_online "${WORK}/b-poll.json"; then
        B_OK=0
        break
      fi
      get_node "${NODE_A_ID}" "${WORK}/a-poll.json" >/dev/null || true
      aver="$(node_field "${WORK}/a-poll.json" serverVersion)"
      [[ -z "${aver}" ]] && aver="$(node_field "${WORK}/a-poll.json" version)"
      astatus="$(node_field "${WORK}/a-poll.json" lastCommandStatus)"
      if [[ "${aver}" == "${TARGET_VERSION}" ]] || [[ "${astatus,,}" == "succeeded" || "${astatus,,}" == "success" || "${astatus,,}" == "completed" ]]; then
        break
      fi
      sleep "${POLL_SECONDS}"
    done
    if [[ "${B_OK}" -eq 1 ]]; then
      record TWO_HEALTHY_B_STABLE PASS "B remained healthy/online/accepting during A update"
    else
      record TWO_HEALTHY_B_STABLE FAIL "B lost healthy/online/accepting during A update"
    fi
    if wait_out="$(wait_node_version "${NODE_A_ID}" "${TARGET_VERSION}")"; then
      record TWO_HEALTHY_A_VERSION PASS "${wait_out}"
      record TWO_HEALTHY PASS "A updated; B stable"
    else
      record TWO_HEALTHY_A_VERSION FAIL "${wait_out}"
      record TWO_HEALTHY FAIL "A did not reach ${TARGET_VERSION}"
    fi
  fi
fi

# =============================================================================
# 3) CONCURRENT: while A update active (or re-trigger), try update B → BLOCK
# =============================================================================
log "--- scenario CONCURRENT ---"
# If A already finished, start a fresh update on A only if versions allow; else
# force a concurrent attempt by draining path: enqueue A again when possible.
# Practical approach: drain neither; if A is still mid-update, enqueue B.
# If A finished, enqueue A (may no-op/already_current) then immediately B — if
# CP treats only Pending/Running as active, re-enqueue may fail. Prefer:
# temporarily mark A update by enqueue when B healthy; if A already at target,
# use NODE_B as the first update target for concurrent probe after resetting.

get_node "${NODE_A_ID}" "${WORK}/a-conc.json" >/dev/null
get_node "${NODE_B_ID}" "${WORK}/b-conc.json" >/dev/null
aver="$(node_field "${WORK}/a-conc.json" serverVersion)"
[[ -z "${aver}" ]] && aver="$(node_field "${WORK}/a-conc.json" version)"

CONC_FIRST="${NODE_A_ID}"
CONC_SECOND="${NODE_B_ID}"
# Prefer updating the node that is still behind target so command stays active.
if [[ "${aver}" == "${TARGET_VERSION}" ]]; then
  CONC_FIRST="${NODE_B_ID}"
  CONC_SECOND="${NODE_A_ID}"
fi

# Ensure sibling healthy
if ! node_is_healthy_online "${WORK}/a-conc.json" || ! node_is_healthy_online "${WORK}/b-conc.json"; then
  # Try to undrain both
  set_draining "${NODE_A_ID}" false >/dev/null || true
  set_draining "${NODE_B_ID}" false >/dev/null || true
  sleep 2
  get_node "${NODE_A_ID}" "${WORK}/a-conc.json" >/dev/null
  get_node "${NODE_B_ID}" "${WORK}/b-conc.json" >/dev/null
fi

first_code="$(enqueue_update "${CONC_FIRST}")"
cp -f "${WORK}/resp.body" "${WORK}/conc-first.json" || true
if [[ "${first_code}" != "200" && "${first_code}" != "201" && "${first_code}" != "202" ]]; then
  # If already current, concurrent scenario cannot be live-proven → FAIL not PASS
  record CONCURRENT FAIL "could not start active update on ${CONC_FIRST} http=${first_code}"
else
  sleep 1
  second_code="$(enqueue_update "${CONC_SECOND}")"
  cp -f "${WORK}/resp.body" "${WORK}/conc-second.json" || true
  if [[ "${second_code}" == "409" ]] || grep -qi 'conflict\|another disruptive\|already active' "${WORK}/conc-second.json" 2>/dev/null; then
    record CONCURRENT PASS "second update blocked http=${second_code}"
  else
    record CONCURRENT FAIL "expected BLOCK/CONFLICT for second update http=${second_code}"
  fi
  # Wait for first to finish to avoid leaving location disrupted
  wait_node_version "${CONC_FIRST}" "${TARGET_VERSION}" >/dev/null || true
fi

# =============================================================================
# 4) SAME-LOCATION ONLY: cross-location node is not a sibling
# =============================================================================
log "--- scenario SAME_LOCATION_ONLY ---"
if [[ -z "${CROSS_LOCATION_NODE_ID}" ]]; then
  record SAME_LOCATION_ONLY SKIP "CROSS_LOCATION_NODE_ID not provided"
else
  # Drain same-location B so only A remains eligible in LOCATION_ID.
  # Cross-location healthy node must NOT count as sibling → update A blocked.
  set_draining "${NODE_B_ID}" true >/dev/null || true
  sleep 2
  get_node "${CROSS_LOCATION_NODE_ID}" "${WORK}/cross.json" >/dev/null || true
  cross_loc="$(node_field "${WORK}/cross.json" locationId)"
  if [[ -n "${cross_loc}" && "${cross_loc}" == "${LOCATION_ID}" ]]; then
    record SAME_LOCATION_ONLY FAIL "cross node locationId=${cross_loc} matches LOCATION_ID (not cross-location)"
  else
    upd_code="$(enqueue_update "${NODE_A_ID}")"
    cp -f "${WORK}/resp.body" "${WORK}/cross-upd.json" || true
    if [[ "${upd_code}" == "409" ]] || grep -qi 'conflict\|last eligible\|last healthy\|refusing disruptive' "${WORK}/cross-upd.json" 2>/dev/null; then
      record SAME_LOCATION_ONLY PASS "cross-location node not counted as sibling (update blocked)"
    else
      record SAME_LOCATION_ONLY FAIL "expected block when only cross-location peer healthy http=${upd_code}"
    fi
  fi
  set_draining "${NODE_B_ID}" false >/dev/null || true
fi

# =============================================================================
# 5) STATE RESTORE: Enabled/Draining/Maintenance after update
# =============================================================================
log "--- scenario STATE_RESTORE ---"
get_node "${NODE_A_ID}" "${WORK}/a-after.json" >/dev/null
A_ENABLED_AFTER="$(node_field "${WORK}/a-after.json" enabled)"
A_DRAINING_AFTER="$(node_field "${WORK}/a-after.json" draining)"
A_MAINT_AFTER="$(node_field "${WORK}/a-after.json" maintenanceMode)"
[[ -z "${A_MAINT_AFTER}" ]] && A_MAINT_AFTER="$(node_field "${WORK}/a-after.json" maintenance)"

# If we cannot observe fields, FAIL (not PASS)
if [[ -z "${A_ENABLED_BEFORE}${A_DRAINING_BEFORE}${A_MAINT_BEFORE}" ]]; then
  record STATE_RESTORE FAIL "could not read pre-update Enabled/Draining/Maintenance fields from admin GET"
elif [[ -z "${A_ENABLED_AFTER}${A_DRAINING_AFTER}${A_MAINT_AFTER}" ]]; then
  record STATE_RESTORE FAIL "could not read post-update Enabled/Draining/Maintenance fields from admin GET"
else
  ok=1
  detail="before(enabled=${A_ENABLED_BEFORE},draining=${A_DRAINING_BEFORE},maint=${A_MAINT_BEFORE}) after(enabled=${A_ENABLED_AFTER},draining=${A_DRAINING_AFTER},maint=${A_MAINT_AFTER})"
  # Restore policy: post-update operational flags should match pre-update snapshot
  # (update must not leave node stuck draining/maintenance unless it started that way).
  if [[ -n "${A_ENABLED_BEFORE}" && -n "${A_ENABLED_AFTER}" && "${A_ENABLED_BEFORE}" != "${A_ENABLED_AFTER}" ]]; then
    ok=0
  fi
  if [[ -n "${A_DRAINING_BEFORE}" && -n "${A_DRAINING_AFTER}" && "${A_DRAINING_BEFORE}" != "${A_DRAINING_AFTER}" ]]; then
    ok=0
  fi
  if [[ -n "${A_MAINT_BEFORE}" && -n "${A_MAINT_AFTER}" && "${A_MAINT_BEFORE}" != "${A_MAINT_AFTER}" ]]; then
    ok=0
  fi
  if [[ "${ok}" -eq 1 ]]; then
    record STATE_RESTORE PASS "${detail}"
  else
    record STATE_RESTORE FAIL "state not restored to pre-update policy: ${detail}"
  fi
fi

# Summary
log "=== SUMMARY ==="
log "pass=${PASS_N} fail=${FAIL_N} skip=${SKIP_N} not_executed=${NOT_EXECUTED_N}"
log "note=SKIP is not PASS; required scenarios must PASS"

REQUIRED_FAIL=0
for need in CONFIRM_STAGING LOGIN LAST_HEALTHY TWO_HEALTHY CONCURRENT STATE_RESTORE; do
  if ! grep -E "^${need}=PASS" "${REPORT}" >/dev/null 2>&1; then
    # TWO_HEALTHY / LAST_HEALTHY / CONCURRENT / STATE_RESTORE must be PASS
    if grep -E "^${need}=SKIP" "${REPORT}" >/dev/null 2>&1; then
      log "required check skipped (not allowed): ${need}"
      REQUIRED_FAIL=1
    elif grep -E "^${need}=PASS" "${REPORT}" >/dev/null 2>&1; then
      :
    else
      log "required check not PASS: ${need}"
      REQUIRED_FAIL=1
    fi
  fi
done

# SAME_LOCATION_ONLY may SKIP
if grep -E '^SAME_LOCATION_ONLY=FAIL' "${REPORT}" >/dev/null 2>&1; then
  REQUIRED_FAIL=1
fi

if [[ "${REQUIRED_FAIL}" -eq 0 && "${FAIL_N}" -eq 0 ]]; then
  log "REMOTE UPDATE LOCATION GATE RESULT=PASS"
  exit 0
fi
log "REMOTE UPDATE LOCATION GATE RESULT=FAIL"
exit 1
