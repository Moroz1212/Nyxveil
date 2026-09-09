#!/usr/bin/env bash
# Prove curl|bash self-contained installer: ONLY install.sh in a temp dir,
# NYXVEIL_INSTALL_MOCK=1, no sibling systemd/ required.
# Also assert unsigned remote manifest fails closed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }
}

# Prefer WSL bash on Windows hosts when invoked via sh from CI helpers.
need_cmd bash

# jq only required for remote-download path; success path uses --skip-download.
# Fail-closed path dies when mock remote manifest lacks a complete asset contract.
TMP="$(mktemp -d /tmp/nyxveil-curl-installer.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

mkdir -p "${TMP}/alone"
cp -a "${ROOT}/installer/install.sh" "${TMP}/alone/install.sh"
chmod +x "${TMP}/alone/install.sh"

# Ensure no systemd sibling can be resolved from the copy location.
[[ ! -e "${TMP}/alone/../systemd" ]] || rm -rf "${TMP}/alone/../systemd" 2>/dev/null || true
# The alone dir's parent is TMP — confirm no systemd there.
[[ ! -d "${TMP}/systemd" ]] || fail "unexpected systemd dir in fixture parent"

MOCK_ROOT="$(mktemp -d /tmp/nyxveil-mock-root.XXXXXX)"
BIN_DIR="${TMP}/bins"
mkdir -p "${BIN_DIR}/scripts"
printf '#!/bin/sh\necho mock-server\n' > "${BIN_DIR}/nyxveil-server"
printf '#!/bin/sh\necho mock-ctl\n' > "${BIN_DIR}/nyxveilctl"
printf '#!/bin/sh\necho mock-catalog-verify\n' > "${BIN_DIR}/nyxveil-catalog-verify"
printf '#!/bin/sh\necho mock-gate\n' > "${BIN_DIR}/scripts/production-gate.sh"
printf '1.1.2\n' > "${BIN_DIR}/VERSION"
printf '# mock frozen core\n7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n' > "${BIN_DIR}/THIRD_PARTY_CORE.md"
chmod +x "${BIN_DIR}/nyxveil-server" "${BIN_DIR}/nyxveilctl" \
  "${BIN_DIR}/nyxveil-catalog-verify" "${BIN_DIR}/scripts/production-gate.sh"

# Dummy CA for pinned_ca_file write path
CA_FILE="${TMP}/cp-ca.pem"
cat > "${CA_FILE}" <<'EOF'
-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAKHBjQqQ0example
-----END CERTIFICATE-----
EOF
# Use a minimal valid-looking PEM (openssl may not parse; mock skips TLS precheck)
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'MIIB' '-----END CERTIFICATE-----' > "${CA_FILE}"

echo "== mock success: binary-dir, no systemd sibling =="
if NYXVEIL_INSTALL_MOCK=1 NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT}" \
  bash "${TMP}/alone/install.sh" \
    --binary-dir "${BIN_DIR}" \
    --skip-download \
    --control-plane https://42mou.ru:8443 \
    --location hel-1 \
    --name "mock-node" \
    --public-host vpn.example.test \
    --bootstrap-token "test-token-not-real" \
    --control-plane-ca-file "${CA_FILE}" \
    --non-interactive; then
  pass "mock install exit 0"
else
  fail "mock install should exit 0"
fi

if [[ -f "${MOCK_ROOT}/etc/systemd/system/nyxveil-server.service" ]]; then
  pass "embedded server unit written"
else
  fail "server unit missing (embedded heredoc broken?)"
fi
if [[ -f "${MOCK_ROOT}/etc/systemd/system/nyxveil-firewall.service" ]]; then
  pass "embedded firewall unit written"
else
  fail "firewall unit missing"
fi
if grep -q 'Wants=.*nyxveil-firewall' "${MOCK_ROOT}/etc/systemd/system/nyxveil-server.service"; then
  pass "server unit wants firewall"
else
  fail "server unit missing firewall Wants="
fi
if [[ -f "${MOCK_ROOT}/etc/nyxveil/server.json" ]] && grep -q 'pinned_ca_file' "${MOCK_ROOT}/etc/nyxveil/server.json"; then
  pass "pinned_ca_file written"
else
  fail "pinned_ca_file not in server.json"
fi
if [[ -x "${MOCK_ROOT}/usr/local/share/nyxveil/scripts/production-gate.sh" ]]; then
  pass "production-gate.sh installed under share/scripts"
else
  fail "production-gate.sh missing after mock install"
fi
if [[ -x "${MOCK_ROOT}/usr/local/sbin/nyxveil-catalog-verify" ]]; then
  pass "nyxveil-catalog-verify installed"
else
  fail "nyxveil-catalog-verify missing after mock install"
fi
if [[ -f "${MOCK_ROOT}/etc/systemd/system/nyxveil-update.service" ]]; then
  pass "embedded/fallback update service present after binary-dir install"
else
  fail "nyxveil-update.service missing after binary-dir install"
fi
if [[ -f "${MOCK_ROOT}/etc/polkit-1/rules.d/50-nyxveil-management.rules" ]]; then
  pass "embedded/fallback polkit rule present after binary-dir install"
else
  fail "50-nyxveil-management.rules missing after binary-dir install"
fi

echo "== TestCurlInstallerCreatesAllServCommands =="
SERV_CMDS=(status health start stop restart logs version config update uninstall)
for c in "${SERV_CMDS[@]}"; do
  if [[ -x "${MOCK_ROOT}/usr/local/bin/serv_${c}" ]]; then
    pass "serv_${c} installed"
  else
    fail "missing serv_${c}"
  fi
done
if grep -q 'serv_update' "${MOCK_ROOT}/usr/local/bin/serv_update" 2>/dev/null || [[ -x "${MOCK_ROOT}/usr/local/bin/serv_update" ]]; then
  pass "serv_update after curl install"
fi
if [[ -x "${MOCK_ROOT}/usr/local/bin/serv_version" ]]; then
  pass "serv_version after curl install"
fi
# TLS key mode from mock register path
if [[ -f "${MOCK_ROOT}/var/lib/nyxveil/tls.key" ]]; then
  if [[ "$(uname -s)" == "Linux" ]]; then
    mode="$(stat -c '%a' "${MOCK_ROOT}/var/lib/nyxveil/tls.key")"
    if [[ "${mode}" == "600" ]]; then
      pass "mock tls.key mode 0600"
    else
      fail "mock tls.key mode=${mode}"
    fi
  else
    pass "mock tls.key present (Unix mode check skipped on $(uname -s))"
  fi
else
  fail "mock tls.key missing"
fi
if grep -q 'ReadWritePaths=/var/lib/nyxveil /run/nyxveil' "${MOCK_ROOT}/etc/systemd/system/nyxveil-server.service" \
   && ! grep -qE 'ReadWritePaths=.* /etc/nyxveil' "${MOCK_ROOT}/etc/systemd/system/nyxveil-server.service"; then
  pass "unit has no /etc/nyxveil write path"
else
  fail "unit still writable /etc/nyxveil"
fi

# Prove we did not need repo systemd/ next to install.sh
if [[ ! -d "${TMP}/alone/systemd" ]]; then
  pass "no systemd/ sibling required next to install.sh"
else
  fail "systemd/ sibling present unexpectedly"
fi

echo "== curl|bash pipe --help (BASH_SOURCE unbound) =="
PIPE_OUT="$(mktemp)"
set +e
cat "${TMP}/alone/install.sh" | bash -s -- --help >"${PIPE_OUT}" 2>&1
pipe_rc=$?
set -e
if [[ "${pipe_rc}" -eq 0 ]] && ! grep -q 'BASH_SOURCE' "${PIPE_OUT}"; then
  pass "curl pipe --help exit 0 without BASH_SOURCE noise"
else
  fail "curl pipe --help rc=${pipe_rc}"
  cat "${PIPE_OUT}" >&2 || true
fi
rm -f "${PIPE_OUT}"

echo "== mock fail-closed: incomplete remote download manifest =="
MOCK_ROOT2="$(mktemp -d /tmp/nyxveil-mock-root2.XXXXXX)"
INCOMPLETE_MANIFEST="$(mktemp /tmp/nyxveil-incomplete-manifest.XXXXXX.json)"
printf '%s\n' '{"version":"1.1.11","arch":"linux/amd64"}' > "${INCOMPLETE_MANIFEST}"
set +e
NYXVEIL_VERSION=1.1.11 \
NYXVEIL_INSTALL_MOCK=1 NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT2}" \
NYXVEIL_INSTALL_MOCK_MANIFEST="${INCOMPLETE_MANIFEST}" \
  bash "${TMP}/alone/install.sh" \
    --control-plane https://example.test \
    --location hel-1 \
    --name "mock-node" \
    --public-host vpn.example.test \
    --bootstrap-token "tok" \
    --non-interactive \
    >/tmp/nyxveil-unsigned-out.txt 2>&1
rc=$?
set -e
rm -f "${INCOMPLETE_MANIFEST}"
if [[ "${rc}" -ne 0 ]]; then
  pass "incomplete remote manifest download dies (rc=${rc})"
else
  fail "incomplete remote manifest should fail closed"
fi
if grep -qiE 'missing fields|sha256|fail-closed|invalid|checksum|manifest|asset' /tmp/nyxveil-unsigned-out.txt; then
  pass "error mentions manifest/integrity failure"
else
  fail "expected manifest/integrity error in output"
  cat /tmp/nyxveil-unsigned-out.txt >&2 || true
fi

# Public host required in production
MOCK_ROOT3="$(mktemp -d /tmp/nyxveil-mock-root3.XXXXXX)"
set +e
NYXVEIL_INSTALL_MOCK=1 NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT3}" \
  bash "${TMP}/alone/install.sh" \
    --binary-dir "${BIN_DIR}" --skip-download \
    --control-plane https://example.test \
    --location hel-1 --name n --bootstrap-token t \
    --non-interactive \
    >/tmp/nyxveil-nohost-out.txt 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "missing public_host dies in production"
else
  fail "missing public_host should die"
fi

rm -rf "${MOCK_ROOT}" "${MOCK_ROOT2}" "${MOCK_ROOT3}"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-curl-installer FAILED" >&2
  exit 1
fi
echo "test-curl-installer PASSED"
