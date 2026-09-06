#!/usr/bin/env bash
# Assert Frozen Core SHA matches server gate (no Core edits).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPECTED="7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b"
SERVER_ASSERT="$(cd "${ROOT}/../../server" && pwd)/scripts/assert-frozen-core.sh"
if [[ -f "${SERVER_ASSERT}" ]]; then
  bash "${SERVER_ASSERT}"
else
  echo "assert-frozen-core.sh missing" >&2
  exit 1
fi
echo "client-windows: Frozen Core SHA OK ${EXPECTED}"
