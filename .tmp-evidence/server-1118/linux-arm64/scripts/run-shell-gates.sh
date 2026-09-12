#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
for s in \
  test-install \
  test-curl-installer \
  test-manifest-interop \
  test-linux-permissions \
  test-bounded-runuser-timeout \
  test-remote-update-gate-contract \
  test-remote-cert-renewal-gate-contract \
  test-bounded-http-get \
  test-pinned-release-installer \
  test-clean-host-gate-contract
do
  echo "== ${s} =="
  bash "scripts/${s}.sh"
done
echo ALL_SHELL_OK
