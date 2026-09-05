#!/usr/bin/env bash
# Safe structure checks for installer packaging.
# Runs on Linux CI and on Windows via Git Bash (no root / no apt).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

fail=0
check() {
  local desc="$1"
  shift
  if "$@"; then
    echo "OK  ${desc}"
  else
    echo "FAIL ${desc}" >&2
    fail=1
  fi
}

echo "== bash -n syntax =="
for f in installer/install.sh installer/uninstall.sh scripts/*.sh; do
  [[ -f "${f}" ]] || continue
  check "bash -n ${f}" bash -n "${f}"
done

echo "== required files =="
required=(
  installer/install.sh
  installer/uninstall.sh
  systemd/nyxveil-server.service
  firewall/nftables-nyxveil.conf
  scripts/build-release.sh
  scripts/package-release.sh
  scripts/test-install.sh
  scripts/serv_wrappers.sh
  README.md
  THIRD_PARTY_CORE.md
  VERSION
  docs/ARCHITECTURE.md
  docs/INSTALL.md
  docs/CONTROL-PLANE.md
  docs/NODE-AUTH.md
  docs/NETWORKING.md
  docs/FIREWALL.md
  docs/UPDATE.md
  docs/TROUBLESHOOTING.md
  docs/RESOURCE-BUDGET.md
  docs/SECURITY.md
  docs/CLEAN-HOST-TEST.md
)
for f in "${required[@]}"; do
  check "exists ${f}" test -f "${f}"
done

echo "== content guards =="
check "install never flushes ruleset" \
  bash -c '! grep -E "^[[:space:]]*nft[[:space:]]+flush[[:space:]]+ruleset" installer/install.sh installer/uninstall.sh firewall/nftables-nyxveil.conf'
check "install has EXIT trap rollback" \
  grep -q 'trap on_exit EXIT' installer/install.sh
check "install preserves node.key" \
  grep -q 'node.key' installer/install.sh
check "install uses read -s for token" \
  grep -q 'read -r -s' installer/install.sh
check "unit User=nyxveil" \
  grep -q '^User=nyxveil' systemd/nyxveil-server.service
check "unit CAP_NET_ADMIN" \
  grep -q 'CAP_NET_ADMIN' systemd/nyxveil-server.service
check "Frozen Core SHA documented" \
  grep -q '7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b' THIRD_PARTY_CORE.md

echo "== LF line endings (shell scripts) =="
if command -v file >/dev/null 2>&1; then
  for f in installer/*.sh scripts/*.sh; do
    if grep -q $'\r' "${f}"; then
      echo "FAIL CRLF in ${f}" >&2
      fail=1
    else
      echo "OK  LF ${f}"
    fi
  done
else
  for f in installer/*.sh scripts/*.sh; do
    if grep -q $'\r' "${f}"; then
      echo "FAIL CRLF in ${f}" >&2
      fail=1
    else
      echo "OK  LF ${f}"
    fi
  done
fi

if [[ "${fail}" -ne 0 ]]; then
  echo "test-install FAILED" >&2
  exit 1
fi
echo "test-install PASSED"
