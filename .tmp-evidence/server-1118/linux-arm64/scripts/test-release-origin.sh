#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
eval "$(sed -n '/^assert_release_asset_origin()/,/^}/p' "${ROOT}/installer/install.sh")"
die() { exit 1; }
MOCK=0 NYXVEIL_TEST_MODE=0
base=https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.1.12
assert_release_asset_origin "${base}/nyxveil-server-linux-amd64" "${base}"
for bad in https://evil.test/asset "${base}/../other" "${base}/asset?redirect=evil" "${base}/%2e%2e"; do
  if (assert_release_asset_origin "${bad}" "${base}"); then echo 'untrusted source accepted'; exit 1; fi
done
NYXVEIL_TEST_MODE=1
assert_release_asset_origin http://127.0.0.1/test "${base}"
if NYXVEIL_TEST_MODE=0 bash "${ROOT}/scripts/live-final-update.sh" --base-url http://127.0.0.1:1 --verify-chain >/dev/null 2>&1; then
  echo 'production helper accepted test origin'; exit 1
fi
echo 'RELEASE_ORIGIN=PASS'
