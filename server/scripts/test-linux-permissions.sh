#!/usr/bin/env bash
# Linux permission / ownership regression for clean-host install layout.
# Runs on Ubuntu (GitHub Actions). Skips gracefully on non-Linux.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "test-linux-permissions: SKIP (not Linux)"
  exit 0
fi

FAIL=0
pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

echo "== installer / unit contract =="
grep -q 'run_as_nyxveil' installer/install.sh && pass "install registers via run_as_nyxveil" || fail "missing run_as_nyxveil"
grep -q 'fix_state_ownership' installer/install.sh && pass "fix_state_ownership present" || fail "missing fix_state_ownership"
if grep -E 'ReadWritePaths=.* /etc/nyxveil' installer/install.sh systemd/nyxveil-server.service; then
  fail "ReadWritePaths must not include /etc/nyxveil"
else
  pass "ReadWritePaths excludes /etc/nyxveil"
fi
grep -q 'ReadWritePaths=/var/lib/nyxveil /run/nyxveil' systemd/nyxveil-server.service \
  && pass "unit write paths limited to state+run" || fail "unexpected ReadWritePaths"

echo "== mock install ownership modes =="
TMP="$(mktemp -d /tmp/nyxveil-perm.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT
MOCK_ROOT="${TMP}/root"
BIN_DIR="${TMP}/bins"
mkdir -p "${BIN_DIR}"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/nyxveil-server"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/nyxveilctl"
chmod +x "${BIN_DIR}/"*
CA="${TMP}/ca.pem"
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'MIIB' '-----END CERTIFICATE-----' > "${CA}"

NYXVEIL_INSTALL_MOCK=1 NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT}" \
  bash installer/install.sh \
    --binary-dir "${BIN_DIR}" --skip-download \
    --control-plane https://example.test \
    --location hel-1 --name n --public-host vpn.example.test \
    --bootstrap-token t --control-plane-ca-file "${CA}" \
    --non-interactive >/dev/null

STATE="${MOCK_ROOT}/var/lib/nyxveil"
[[ -f "${STATE}/node.key" ]] || fail "node.key missing"
[[ -f "${STATE}/tls.key" ]] || fail "tls.key missing"
[[ -f "${STATE}/tls.crt" ]] || fail "tls.crt missing"

if [[ "$(uname -s)" == "Linux" ]]; then
  mode_of() { stat -c '%a' "$1"; }
  [[ "$(mode_of "${STATE}")" == "700" ]] && pass "state dir 0700" || fail "state dir mode $(mode_of "${STATE}")"
  [[ "$(mode_of "${STATE}/node.key")" == "600" ]] && pass "node.key 0600" || fail "node.key mode"
  [[ "$(mode_of "${STATE}/tls.key")" == "600" ]] && pass "tls.key 0600" || fail "tls.key mode"
  perm="$(mode_of "${STATE}/tls.key")"
  [[ "${perm: -1}" == "0" ]] && pass "tls.key not world-readable" || fail "tls.key world bits"
else
  pass "Unix mode checks deferred to Linux CI (host=$(uname -s))"
fi

ETC_JSON="${MOCK_ROOT}/etc/nyxveil/server.json"
[[ -f "${ETC_JSON}" ]] && pass "server.json present" || fail "server.json missing"
grep -q 'ReadWritePaths=/var/lib/nyxveil /run/nyxveil' "${MOCK_ROOT}/etc/systemd/system/nyxveil-server.service" \
  && pass "embedded unit excludes /etc write" || fail "embedded unit ReadWritePaths"

echo "== Go TLS generate under non-root uid simulation =="
# Prove generateSelfSigned / register material is readable by the creating user (0600).
go test -timeout 60s -run 'TestGenerateSelfSignedIPUsesIPAddresses|TestAppliedConfigPersistenceAsServiceUser|TestServiceDoesNotWriteEtcConfig|TestLocationChangeSurvivesRestart|TestConfigVersionSurvivesRestart' ./internal/runtime/ \
  && pass "Go ownership/config persistence tests" || fail "Go persistence tests"

echo "== applied-config writable under state =="
# Unit-level: SaveApplied into a 0700 dir succeeds; /etc remains untouched.
go test -timeout 30s -run TestServiceDoesNotWriteEtcConfig ./internal/runtime/ \
  && pass "daemon does not write etc" || fail "etc write test"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-linux-permissions FAILED" >&2
  exit 1
fi
echo "test-linux-permissions PASSED"
