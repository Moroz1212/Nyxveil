#!/usr/bin/env bash
# Contract test: remote-update-location-gate.sh field paths match NodeAdminStatusResponse snake_case.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="${ROOT}/scripts/remote-update-location-gate.sh"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

[[ -f "${GATE}" ]] || { echo "missing ${GATE}" >&2; exit 1; }

# Same property names as licensing NodeAdminStatusResponseContractTests / AdminContracts.cs
REQUIRED_FIELDS=(
  node_id
  location_id
  enabled
  draining
  maintenance_mode
  healthy
  accepting
  online
  last_seen_at
  current_sessions
  reported_server_version
  config_version
  lifecycle_state
)

TMP="$(mktemp -d /tmp/nyxveil-gate-contract.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

NOW_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf '2026-09-09T12:00:00Z')"

cat >"${TMP}/fixture.json" <<EOF
{
  "node_id": "node-1",
  "location_id": "loc-1",
  "enabled": true,
  "draining": false,
  "maintenance_mode": false,
  "healthy": true,
  "accepting": true,
  "online": true,
  "last_seen_at": "${NOW_UTC}",
  "current_sessions": 3,
  "reported_server_version": "1.1.12",
  "config_version": 7,
  "lifecycle_state": "Active"
}
EOF

# Prefer jq; otherwise a working python with json. Windows Store stubs are ignored.
HAVE_JSON=0
if command -v jq >/dev/null 2>&1; then
  HAVE_JSON=1
elif command -v python3 >/dev/null 2>&1 && python3 -c 'import json' >/dev/null 2>&1; then
  HAVE_JSON=1
elif command -v python >/dev/null 2>&1 && python -c 'import json' >/dev/null 2>&1; then
  HAVE_JSON=1
fi

json_get() {
  local file="$1" expr="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r "${expr}" "${file}" 2>/dev/null || true
    return
  fi
  local py=""
  if command -v python3 >/dev/null 2>&1 && python3 -c 'import json' >/dev/null 2>&1; then
    py=python3
  elif command -v python >/dev/null 2>&1 && python -c 'import json' >/dev/null 2>&1; then
    py=python
  fi
  [[ -n "${py}" ]] || return 0
  "${py}" - "${file}" "${expr}" <<'PY' 2>/dev/null || true
import json, sys
path, expr = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    d = json.load(f)
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

# Flat-JSON fallback for hosts without jq/python (Git Bash on Windows).
json_get_flat() {
  local file="$1" field="$2"
  local line val
  line="$(grep -E "\"${field}\"[[:space:]]*:" "${file}" | head -n1 || true)"
  [[ -n "${line}" ]] || { printf ''; return; }
  val="$(printf '%s' "${line}" | sed -n 's/.*"'"${field}"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [[ -n "${val}" ]]; then
    printf '%s' "${val}"
    return
  fi
  if printf '%s' "${line}" | grep -Eq ':[[:space:]]*(true|false)'; then
    printf '%s' "${line}" | grep -oE 'true|false' | head -n1
    return
  fi
  printf '%s' "${line}" | sed -n 's/.*"'"${field}"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -n1
}

node_field() {
  local file="$1" field="$2"
  local got=""
  if [[ "${HAVE_JSON}" -eq 1 ]]; then
    got="$(json_get "${file}" ".${field}")"
  fi
  if [[ -z "${got}" || "${got}" == "null" ]]; then
    got="$(json_get_flat "${file}" "${field}")"
  fi
  printf '%s' "${got}"
}

echo "== field resolution against fixture (same jq paths as gate) =="
for f in "${REQUIRED_FIELDS[@]}"; do
  got="$(node_field "${TMP}/fixture.json" "${f}")"
  if [[ -z "${got}" || "${got}" == "null" ]]; then
    fail "field ${f} unresolved"
  else
    pass "field ${f}=${got}"
  fi
done

echo "== gate script uses snake_case contract fields =="
for f in "${REQUIRED_FIELDS[@]}"; do
  if grep -q "${f}" "${GATE}"; then
    pass "gate references ${f}"
  else
    fail "gate missing reference to ${f}"
  fi
done

echo "== gate must not use camelCase heuristics =="
for name in runtimeStatus serverVersion locationId maintenanceMode; do
  if grep -F "${name}" "${GATE}" >/dev/null 2>&1; then
    fail "forbidden camelCase/heuristic still present: ${name}"
  else
    pass "no ${name}"
  fi
done

# Soft-fail patterns that might appear in comments — require node_field accessors absent.
if grep -E 'node_field[[:space:]]+"\$\{[^}]+\}"[[:space:]]+(status|health|version)([[:space:]]|$)' "${GATE}" >/dev/null 2>&1; then
  fail "heuristic node_field status/health/version still used"
else
  pass "no heuristic status/health/version node_field"
fi

if grep -q 'node_is_healthy_online' "${GATE}" && grep -q 'API_CONTRACT' "${GATE}" && grep -q 'last_seen_is_fresh' "${GATE}"; then
  pass "healthy/online requires contract + freshness helpers"
else
  fail "missing node_is_healthy_online / API_CONTRACT / last_seen_is_fresh"
fi

if grep -q 'reported_server_version' "${GATE}" && grep -q 'current_sessions' "${GATE}"; then
  pass "version/sessions use reported_server_version and current_sessions"
else
  fail "version/sessions fields not wired"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-remote-update-gate-contract FAILED" >&2
  exit 1
fi
echo "test-remote-update-gate-contract PASSED"
