#!/usr/bin/env bash
# Contract: clean-host gate must require management assets (not SKIP) and
# stronger TLS / status field checks.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="${ROOT}/scripts/clean-host-install-gate.sh"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

[[ -f "${GATE}" ]] || { echo "missing gate" >&2; exit 1; }

if grep -q 'MGMT_POLKIT SKIP' "${GATE}"; then
  fail "MGMT_POLKIT must not SKIP — remote management is required"
else
  pass "MGMT_POLKIT is not optional SKIP"
fi

if grep -q 'record MGMT_POLKIT FAIL' "${GATE}"; then
  pass "MGMT_POLKIT FAIL on absence"
else
  fail "MGMT_POLKIT must FAIL when rules missing"
fi

for needle in \
  TLS_KEY_MATCH TLS_VALIDITY SERVED_SPKI LISTENER_TCP_443 \
  TUNReady TicketKeysLoaded CPConnected IdentityPresent \
  NotBefore NotAfter THIRD_PARTY CATALOG_VERIFY \
  ACME_PRIVILEGED_BIND NFTABLES_IDEMPOTENCY ACME_NO_PERSISTENT_SETCAP; do
  if grep -q "${needle}" "${GATE}"; then
    pass "mentions ${needle}"
  else
    fail "missing check: ${needle}"
  fi
done

if grep -E 'for need in' -A20 "${GATE}" | grep -q 'MGMT_POLKIT'; then
  pass "MGMT_POLKIT in required PASS list"
else
  fail "MGMT_POLKIT not in required PASS loop"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "CLEAN_HOST_GATE_CONTRACT=FAIL"
  exit 1
fi
echo "CLEAN_HOST_GATE_CONTRACT=PASS"
exit 0
