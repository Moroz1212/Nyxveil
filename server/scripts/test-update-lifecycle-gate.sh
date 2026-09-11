#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
# Execute the actual installed-gate health block with deterministic status inputs.
sed -n '/^json_bool() {/,/^CERT=/p' "${ROOT}/scripts/production-gate.sh" | sed '$d' >"${WORK}/health.sh"
export WORK
record() { :; }
fail() { echo "FAIL $1" >&2; exit 1; }
export -f record fail
for scenario in drained maintenance active bad_identity bad_tun bad_bridge bad_cp bad_tickets bad_revocation bad_version; do
  python3 - "${WORK}/status.json" "${scenario}" <<'PY'
import json,sys
s=dict(running=True,tun_ready=True,bridge_ok=True,ticket_keys_loaded=True,
       identity_present=True,cp_connected=True,draining=True,accepting=False,
       tls_ok=False,quic_ok=False,version_blocked=False,revocation_stale=False)
case=sys.argv[2]
if case=='maintenance': s.update(draining=False,maintenance_mode=True)
if case=='active': s.update(draining=False,accepting=True)
for name,field in [('identity','identity_present'),('tun','tun_ready'),('bridge','bridge_ok'),('cp','cp_connected'),('tickets','ticket_keys_loaded')]:
    if case=='bad_'+name:s[field]=False
if case=='bad_revocation':s['revocation_stale']=True
if case=='bad_version':s['version_blocked']=True
with open(sys.argv[1],'w') as f:json.dump(s,f)
PY
  if MODE=updater bash "${WORK}/health.sh" >"${WORK}/result" 2>&1; then
    case "${scenario}" in drained|maintenance) ;; *) echo "unexpected PASS ${scenario}"; exit 1;; esac
  else
    case "${scenario}" in drained|maintenance) cat "${WORK}/result"; exit 1;; esac
  fi
  if [[ "${scenario}" == drained ]] && MODE=local bash "${WORK}/health.sh" >"${WORK}/result" 2>&1; then
    echo 'local gate incorrectly allowed stopped listeners'; exit 1
  fi
done
echo 'UPDATE_LIFECYCLE_GATE=PASS (health block fixture; not live E2E)'
