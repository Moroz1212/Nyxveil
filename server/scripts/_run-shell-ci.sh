#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FAIL=0
for s in \
  test-curl-installer \
  test-manifest-interop \
  test-linux-permissions \
  test-installer-version-resolution \
  test-pinned-release-installer \
  test-remote-update-gate-contract \
  test-remote-cert-renewal-gate-contract \
  test-bounded-http-get \
  test-installer-management-assets \
  test-nftables-idempotency \
  test-acme-privileged-bind \
  test-clean-host-gate-contract \
  test-bounded-runuser-timeout \
  test-post-registration-identity
do
  echo "== ${s} =="
  if bash "scripts/${s}.sh"; then
    echo "PASS ${s}"
  else
    echo "FAIL ${s}"
    FAIL=1
  fi
done
exit "${FAIL}"
