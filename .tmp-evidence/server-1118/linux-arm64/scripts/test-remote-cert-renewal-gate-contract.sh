#!/usr/bin/env bash
# Contract checks for remote-certificate-renewal-gate.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="${ROOT}/scripts/remote-certificate-renewal-gate.sh"
FAIL=0
pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

[[ -f "${GATE}" ]] || { echo "missing ${GATE}" >&2; exit 1; }
bash -n "${GATE}" || fail "bash -n"

grep -q 'RenewCertificate' "${GATE}" || fail "must enqueue RenewCertificate"
grep -q 'certificate_renew' "${GATE}" || fail "must check certificate_renew capability"
grep -Fq -- '--admin-password)' "${GATE}" || fail "must reject argv password"
grep -q 'REPORT_JSON' "${GATE}" || fail "must emit JSON report"
grep -q 'rate_limited' "${GATE}" || fail "must accept rate_limited"
grep -q 'renewed' "${GATE}" || fail "must accept renewed"
grep -q -- '--connect-timeout' "${GATE}" || fail "bounded curl"
grep -q -- '--max-time' "${GATE}" || fail "bounded curl max-time"

# Must not echo password
if grep -E 'echo.*ADMIN_PASSWORD|printf.*ADMIN_PASSWORD' "${GATE}" | grep -v 'password acquired' >/dev/null; then
  fail "must not echo password"
else
  pass "no password echo"
fi

[[ "${FAIL}" -eq 0 ]]
echo "test-remote-cert-renewal-gate-contract PASSED"
