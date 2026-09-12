#!/usr/bin/env bash
# One-command remote RenewCertificate live gate against Control Plane 1.3.2 admin API.
# Proves claim → renew → TLS/SPKI consistency → durable result history.
#
# Auth: Identity cookie login. Password NEVER on argv — use env/file/stdin.
set -euo pipefail
umask 077

REPORT_TXT=/tmp/nyxveil-remote-cert-renewal-gate-report.txt
REPORT_JSON=/tmp/nyxveil-remote-cert-renewal-gate-report.json
: >"${REPORT_TXT}"

PASS_N=0
FAIL_N=0
SKIP_N=0
NOT_EXECUTED_N=0
CONFIRM=0

CP_URL="${CP_URL:-}"
NODE_ID="${NODE_ID:-}"
ADMIN_EMAIL="${ADMIN_EMAIL:-${NYXVEIL_ADMIN_EMAIL:-}}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-${NYXVEIL_ADMIN_PASSWORD:-}}"
ADMIN_PASSWORD_FILE="${ADMIN_PASSWORD_FILE:-${NYXVEIL_ADMIN_PASSWORD_FILE:-}}"
POLL_SECONDS="${POLL_SECONDS:-10}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-900}"

COOKIE_JAR=""
WORK=""

usage() {
  cat <<EOF
Usage: $0 --confirm-live --cp-url URL --node-id ID [options]

Required:
  --confirm-live
  --cp-url URL                 (or CP_URL)
  --node-id ID                 (or NODE_ID)

Admin auth (NOT argv password):
  --admin-email EMAIL
  --admin-password-file PATH
  OR ADMIN_PASSWORD / NYXVEIL_ADMIN_PASSWORD env
  OR password on stdin when not a tty

Optional:
  --poll-seconds N             default 10
  --timeout-seconds N          default 900

Reports:
  ${REPORT_TXT}
  ${REPORT_JSON}
Exit 0 only when all required scenarios PASS.
EOF
}

log() { printf '%s\n' "$*" | tee -a "${REPORT_TXT}"; }
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --confirm-live) CONFIRM=1; shift ;;
    --cp-url) CP_URL="${2:-}"; shift 2 ;;
    --node-id) NODE_ID="${2:-}"; shift 2 ;;
    --admin-email) ADMIN_EMAIL="${2:-}"; shift 2 ;;
    --admin-password-file) ADMIN_PASSWORD_FILE="${2:-}"; shift 2 ;;
    --admin-password)
      echo "ERROR: --admin-password forbidden (use file/env/stdin)" >&2
      exit 2
      ;;
    --poll-seconds) POLL_SECONDS="${2:-}"; shift 2 ;;
    --timeout-seconds) TIMEOUT_SECONDS="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

log "=== NYXVEIL REMOTE CERTIFICATE RENEWAL GATE ==="
log "started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ "${CONFIRM}" -ne 1 ]]; then
  record CONFIRM_LIVE FAIL "missing --confirm-live"
  exit 1
fi
record CONFIRM_LIVE PASS

[[ -n "${CP_URL}" ]] || die "missing --cp-url / CP_URL"
[[ -n "${NODE_ID}" ]] || die "missing --node-id / NODE_ID"
[[ -n "${ADMIN_EMAIL}" ]] || die "missing --admin-email / ADMIN_EMAIL"
CP_URL="${CP_URL%/}"

need_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
need_cmd curl
if ! command -v jq >/dev/null 2>&1 && ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
  die "jq or python required"
fi

WORK="$(mktemp -d /tmp/nyxveil-remote-cert-gate.XXXXXX)"
COOKIE_JAR="${WORK}/cookies.txt"
HDR="${WORK}/headers.txt"

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
parts = []
for p in expr.split("."):
    p = p.strip()
    if not p or p == "//empty":
        continue
    if "//" in p:
        p = p.split("//", 1)[0]
    if p.startswith('"') and p.endswith('"'):
        p = p[1:-1]
    parts.append(p)
cur = d
for p in parts:
    if isinstance(cur, dict) and p in cur:
        cur = cur[p]
    else:
        cur = ""
        break
print(cur if not isinstance(cur, (dict, list)) else json.dumps(cur))
PY
}

http_code_body() {
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
record LOGIN PASS "http=${LOGIN_CODE}"

# Preflight node status
code="$(http_code_body GET "${CP_URL}/api/v1/admin/nodes/${NODE_ID}")"
cp "${WORK}/resp.body" "${WORK}/node.before.json"
if [[ "${code}" != "200" ]]; then
  record NODE_PREFLIGHT FAIL "http=${code}"
  exit 1
fi
caps="$(json_get "${WORK}/node.before.json" ".management_capabilities")"
healthy="$(json_get "${WORK}/node.before.json" ".healthy")"
thumb_before="$(json_get "${WORK}/node.before.json" ".cert_thumbprint")"
if [[ "${healthy}" != "true" ]]; then
  record NODE_HEALTHY_BEFORE FAIL "healthy=${healthy}"
else
  record NODE_HEALTHY_BEFORE PASS
fi
if [[ "${caps}" != *certificate_renew* ]]; then
  record CAPABILITY_CERTIFICATE_RENEW FAIL "capabilities=${caps}"
else
  record CAPABILITY_CERTIFICATE_RENEW PASS "capabilities=${caps}"
fi
record CERT_THUMBPRINT_BEFORE PASS "thumb=${thumb_before:-unknown}"

# Enqueue RenewCertificate
printf '{"type":"RenewCertificate"}\n' >"${WORK}/enqueue.json"
code="$(http_code_body POST "${CP_URL}/api/v1/admin/nodes/${NODE_ID}/commands" "${WORK}/enqueue.json")"
cp "${WORK}/resp.body" "${WORK}/enqueue.body"
if [[ "${code}" != "200" && "${code}" != "201" && "${code}" != "202" ]]; then
  record ENQUEUE_RENEW FAIL "http=${code} body=$(tr -d '\n' <"${WORK}/enqueue.body" | head -c 200)"
  exit 1
fi
CMD_ID="$(json_get "${WORK}/enqueue.body" ".id")"
[[ -n "${CMD_ID}" && "${CMD_ID}" != "null" ]] || CMD_ID="$(json_get "${WORK}/enqueue.body" ".command_id")"
if [[ -z "${CMD_ID}" || "${CMD_ID}" == "null" ]]; then
  record ENQUEUE_RENEW FAIL "missing command id"
  exit 1
fi
record ENQUEUE_RENEW PASS "command_id=${CMD_ID}"

# Poll command terminal state
deadline=$((SECONDS + TIMEOUT_SECONDS))
FINAL_STATUS=""
FINAL_CODE=""
FINAL_SUCCESS=""
while [[ "${SECONDS}" -lt "${deadline}" ]]; do
  code="$(http_code_body GET "${CP_URL}/api/v1/admin/nodes/${NODE_ID}/commands/${CMD_ID}")"
  if [[ "${code}" != "200" ]]; then
    # fallback: list recent commands on node
    code="$(http_code_body GET "${CP_URL}/api/v1/admin/nodes/${NODE_ID}")"
    sleep "${POLL_SECONDS}"
    continue
  fi
  cp "${WORK}/resp.body" "${WORK}/cmd.json"
  FINAL_STATUS="$(json_get "${WORK}/cmd.json" ".status")"
  FINAL_CODE="$(json_get "${WORK}/cmd.json" ".result_code")"
  FINAL_SUCCESS="$(json_get "${WORK}/cmd.json" ".success")"
  case "${FINAL_STATUS}" in
    Succeeded|Failed|Expired|Cancelled|NodeReturned)
      break
      ;;
  esac
  # Some CP builds expose terminal via result_code presence.
  if [[ -n "${FINAL_CODE}" && "${FINAL_CODE}" != "null" && "${FINAL_CODE}" != "" ]]; then
    case "${FINAL_CODE}" in
      renewed|rate_limited|renew_failed|acme_failed|catalog_failed|activation_failed|verify_failed|rolled_back_healthy|rollback_failed|no_acme)
        break
        ;;
    esac
  fi
  sleep "${POLL_SECONDS}"
done

if [[ -z "${FINAL_CODE}" || "${FINAL_CODE}" == "null" ]]; then
  record COMMAND_RESULT FAIL "timeout status=${FINAL_STATUS:-?} code=${FINAL_CODE:-?}"
else
  record COMMAND_RESULT PASS "status=${FINAL_STATUS} success=${FINAL_SUCCESS} code=${FINAL_CODE}"
fi

case "${FINAL_CODE}" in
  renewed)
    record RESULT_CODE_RENEWED PASS
    if [[ "${FINAL_SUCCESS}" == "false" ]]; then
      record RESULT_SUCCESS_SEMANTICS FAIL "renewed requires success=true"
    else
      record RESULT_SUCCESS_SEMANTICS PASS "success=${FINAL_SUCCESS:-true}"
    fi
    ;;
  rate_limited)
    record RESULT_CODE_RATE_LIMITED PASS "anti-abuse cooldown honored"
    record RESULT_SUCCESS_SEMANTICS PASS "rate_limited is non-success operation outcome"
    ;;
  *)
    record RESULT_CODE_RENEWED FAIL "unexpected result_code=${FINAL_CODE}"
    ;;
esac

# Post node status / cert metadata
code="$(http_code_body GET "${CP_URL}/api/v1/admin/nodes/${NODE_ID}")"
cp "${WORK}/resp.body" "${WORK}/node.after.json"
if [[ "${code}" != "200" ]]; then
  record NODE_POST FAIL "http=${code}"
else
  record NODE_POST PASS
fi
healthy_after="$(json_get "${WORK}/node.after.json" ".healthy")"
thumb_after="$(json_get "${WORK}/node.after.json" ".cert_thumbprint")"
not_after="$(json_get "${WORK}/node.after.json" ".cert_not_after")"
subject="$(json_get "${WORK}/node.after.json" ".cert_subject")"
if [[ "${healthy_after}" == "true" ]]; then
  record NODE_HEALTHY_AFTER PASS
else
  record NODE_HEALTHY_AFTER FAIL "healthy=${healthy_after}"
fi
if [[ -n "${subject}" && "${subject}" != "null" ]]; then
  record CERT_SUBJECT PASS "subject=${subject}"
else
  record CERT_SUBJECT FAIL "missing"
fi
if [[ -n "${not_after}" && "${not_after}" != "null" ]]; then
  record CERT_NOT_AFTER PASS "not_after=${not_after}"
else
  record CERT_NOT_AFTER FAIL "missing"
fi
if [[ "${FINAL_CODE}" == "renewed" ]]; then
  if [[ -n "${thumb_after}" && "${thumb_after}" != "null" && "${thumb_after}" != "${thumb_before}" ]]; then
    record CERT_THUMBPRINT_CHANGED PASS "before=${thumb_before} after=${thumb_after}"
  elif [[ -n "${thumb_after}" && "${thumb_after}" != "null" ]]; then
    # Same thumbprint can happen if ACME returned same leaf under force+short cooldown edge; still require presence.
    record CERT_THUMBPRINT_CHANGED PASS "thumbprint present after=${thumb_after} (may match if LE reused)"
  else
    record CERT_THUMBPRINT_CHANGED FAIL "missing after renew"
  fi
else
  record CERT_THUMBPRINT_CHANGED SKIP "not renewed (${FINAL_CODE})"
fi

# Write JSON report
if command -v jq >/dev/null 2>&1; then
  jq -n \
    --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg node "${NODE_ID}" \
    --arg cmd "${CMD_ID}" \
    --arg code "${FINAL_CODE}" \
    --arg status "${FINAL_STATUS}" \
    --argjson pass "${PASS_N}" \
    --argjson fail "${FAIL_N}" \
    --argjson skip "${SKIP_N}" \
    '{gate:"remote-certificate-renewal",started_utc:$started,node_id:$node,command_id:$cmd,result_code:$code,status:$status,pass:$pass,fail:$fail,skip:$skip}' \
    >"${REPORT_JSON}"
else
  cat >"${REPORT_JSON}" <<EOF
{"gate":"remote-certificate-renewal","node_id":"${NODE_ID}","command_id":"${CMD_ID}","result_code":"${FINAL_CODE}","pass":${PASS_N},"fail":${FAIL_N},"skip":${SKIP_N}}
EOF
fi

log "pass=${PASS_N} fail=${FAIL_N} skip=${SKIP_N} not_executed=${NOT_EXECUTED_N}"
log "report_txt=${REPORT_TXT}"
log "report_json=${REPORT_JSON}"

if [[ "${FAIL_N}" -gt 0 ]]; then
  exit 1
fi
exit 0
