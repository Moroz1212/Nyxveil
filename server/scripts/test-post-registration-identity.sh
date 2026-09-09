#!/usr/bin/env bash
# Post-registration identity preservation + repair reinstall (mock installer).
# Repair preserves node.key/node_id but still requires a bootstrap token.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0
pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need_cmd bash

TMP="$(mktemp -d /tmp/nyxveil-post-reg.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

BIN_DIR="${TMP}/bins"
mkdir -p "${BIN_DIR}/scripts"
printf '#!/bin/sh\necho mock-server\n' > "${BIN_DIR}/nyxveil-server"
printf '#!/bin/sh\necho mock-ctl\n' > "${BIN_DIR}/nyxveilctl"
printf '#!/bin/sh\necho mock-catalog-verify\n' > "${BIN_DIR}/nyxveil-catalog-verify"
printf '#!/bin/sh\necho mock-gate\n' > "${BIN_DIR}/scripts/production-gate.sh"
printf '1.1.11\n' > "${BIN_DIR}/VERSION"
printf '# mock frozen core\n7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n' > "${BIN_DIR}/THIRD_PARTY_CORE.md"
chmod +x "${BIN_DIR}/nyxveil-server" "${BIN_DIR}/nyxveilctl" \
  "${BIN_DIR}/nyxveil-catalog-verify" "${BIN_DIR}/scripts/production-gate.sh"

CA_FILE="${TMP}/cp-ca.pem"
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'MIIB' '-----END CERTIFICATE-----' > "${CA_FILE}"

INSTALL="${ROOT}/installer/install.sh"

run_mock() {
  local root="$1"
  shift
  NYXVEIL_INSTALL_MOCK=1 NYXVEIL_INSTALL_MOCK_ROOT="${root}" \
    bash "${INSTALL}" \
      --binary-dir "${BIN_DIR}" \
      --skip-download \
      --control-plane https://42mou.ru:8443 \
      --location hel-1 \
      --name "mock-node" \
      --public-host vpn.example.test \
      --control-plane-ca-file "${CA_FILE}" \
      --non-interactive \
      "$@"
}

echo "== pre-registration failure: clean rollback =="
MOCK_PRE="${TMP}/pre"
mkdir -p "${MOCK_PRE}"
set +e
NYXVEIL_INSTALL_FAIL_BEFORE_REGISTER=1 run_mock "${MOCK_PRE}" --bootstrap-token "tok-pre" \
  >"${TMP}/pre.out" 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]] && grep -qi 'rollback complete' "${TMP}/pre.out"; then
  pass "pre-registration failure rolled back"
else
  fail "pre-registration failure should full-rollback"
  cat "${TMP}/pre.out" >&2 || true
fi
if [[ ! -f "${MOCK_PRE}/etc/nyxveil/server.json" ]]; then
  pass "pre-registration rollback removed server.json"
else
  fail "server.json left after pre-registration rollback"
fi
if [[ ! -f "${MOCK_PRE}/var/lib/nyxveil/node.key" ]]; then
  pass "pre-registration rollback has no node.key"
else
  fail "unexpected node.key after pre-registration rollback"
fi
if ! grep -qi 'registration already committed' "${TMP}/pre.out"; then
  pass "pre-registration path did not claim committed identity"
else
  fail "pre-registration incorrectly claimed registration committed"
fi

echo "== post-registration health failure: identity preserved =="
MOCK_POST="${TMP}/post"
mkdir -p "${MOCK_POST}"
set +e
NYXVEIL_INSTALL_FAIL_AFTER_REGISTER=1 run_mock "${MOCK_POST}" --bootstrap-token "tok-post" \
  >"${TMP}/post.out" 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]] && grep -qi 'registration already committed; preserving node identity' "${TMP}/post.out"; then
  pass "post-registration failure preserves identity"
else
  fail "post-registration failure must preserve identity"
  cat "${TMP}/post.out" >&2 || true
fi
if [[ -f "${MOCK_POST}/var/lib/nyxveil/node.key" ]]; then
  pass "node.key preserved"
else
  fail "node.key missing after post-registration rollback"
fi
if [[ -f "${MOCK_POST}/etc/nyxveil/server.json" ]]; then
  pass "server.json preserved"
else
  fail "server.json missing after post-registration rollback"
fi
if [[ -f "${MOCK_POST}/var/lib/nyxveil/applied-config.json" ]]; then
  pass "applied-config preserved"
else
  fail "applied-config.json missing after post-registration rollback"
fi
NODE_ID="$(sed -n 's/.*"node_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${MOCK_POST}/etc/nyxveil/server.json" | head -n1 || true)"
if [[ -n "${NODE_ID}" ]]; then
  pass "node_id present (${NODE_ID})"
else
  fail "node_id missing in preserved server.json"
fi

echo "== next installer repair: requires bootstrap, same node_id =="
set +e
run_mock "${MOCK_POST}" >"${TMP}/repair-noboot.out" 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]] && grep -qi 'bootstrap token required\|missing required value: BOOTSTRAP_TOKEN' "${TMP}/repair-noboot.out"; then
  pass "repair install rejects missing bootstrap token"
else
  fail "repair install must require bootstrap token"
  cat "${TMP}/repair-noboot.out" >&2 || true
fi
if ! grep -qi 'bootstrap token not required\|skipping bootstrap prompt' "${TMP}/repair-noboot.out"; then
  pass "repair path no longer skips bootstrap"
else
  fail "repair still claims bootstrap is optional"
  cat "${TMP}/repair-noboot.out" >&2 || true
fi

set +e
run_mock "${MOCK_POST}" --bootstrap-token "tok-repair" >"${TMP}/repair.out" 2>&1
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  pass "repair install exit 0 with bootstrap token"
else
  fail "repair install should succeed with bootstrap"
  cat "${TMP}/repair.out" >&2 || true
fi
if grep -qi 'identity preserved; bootstrap token still required\|preserving node_id=' "${TMP}/repair.out"; then
  pass "repair preserves identity and still requires bootstrap"
else
  fail "repair did not log identity preservation"
  cat "${TMP}/repair.out" >&2 || true
fi
NODE_ID2="$(sed -n 's/.*"node_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${MOCK_POST}/etc/nyxveil/server.json" | head -n1 || true)"
if [[ -n "${NODE_ID}" && "${NODE_ID}" == "${NODE_ID2}" ]]; then
  pass "same node_id preserved (${NODE_ID2})"
else
  fail "node_id changed: before=${NODE_ID} after=${NODE_ID2}"
fi
if [[ -f "${MOCK_POST}/var/lib/nyxveil/node.key" ]]; then
  pass "node.key still present after repair"
else
  fail "node.key lost after repair"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-post-registration-identity FAILED" >&2
  exit 1
fi
echo "test-post-registration-identity PASSED"
