#!/usr/bin/env bash
# Regression: install.sh version policy — no stale pin; env override; local VERSION;
# optional mocked GitHub stable resolution (no network).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0
INSTALLER="${ROOT}/installer/install.sh"
PRODUCT_VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need_cmd bash

[[ -f "${INSTALLER}" ]] || { echo "missing ${INSTALLER}" >&2; exit 1; }

echo "== static contract =="
if grep -E 'NYXVEIL_VERSION:-[0-9]+\.[0-9]+\.[0-9]+' "${INSTALLER}" >/dev/null 2>&1; then
  pinned="$(grep -Eo 'NYXVEIL_VERSION:-[0-9]+\.[0-9]+\.[0-9]+' "${INSTALLER}" | head -n1 | sed 's/.*:-//')"
  if [[ "${pinned}" != "${PRODUCT_VERSION}" ]]; then
    fail "hardcoded NYXVEIL_VERSION:-${pinned} older/different than VERSION=${PRODUCT_VERSION}"
  else
    pass "pinned default matches VERSION (${PRODUCT_VERSION})"
  fi
else
  pass "no NYXVEIL_VERSION:-X.Y.Z hardcoded default"
fi

if grep -q 'NYXVEIL_VERSION:-1\.1\.10' "${INSTALLER}"; then
  fail "stale pin NYXVEIL_VERSION:-1.1.10 still present"
else
  pass "no stale 1.1.10 version pin"
fi

if grep -q 'resolve_stable_server_version' "${INSTALLER}"; then
  pass "resolve_stable_server_version present"
else
  fail "resolve_stable_server_version missing"
fi

TMP="$(mktemp -d /tmp/nyxveil-ver-resolve.XXXXXX)"
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

run_mock_install() {
  local out="$1"
  shift
  (
    export NYXVEIL_INSTALL_MOCK=1
    export NYXVEIL_INSTALL_MOCK_ROOT
    export NYXVEIL_INSTALL_FAIL_BEFORE_REGISTER=1
    bash "${INSTALLER}" \
      --binary-dir "${BIN_DIR}" \
      --skip-download \
      --control-plane https://42mou.ru:8443 \
      --location hel-1 \
      --name "ver-mock" \
      --public-host vpn.example.test \
      --control-plane-ca-file "${CA_FILE}" \
      --bootstrap-token "tok-ver" \
      --non-interactive \
      "$@"
  ) >"${out}" 2>&1
}

echo "== env override NYXVEIL_VERSION=9.9.9 with mock skip-download =="
MOCK_ROOT="${TMP}/mock-env"
mkdir -p "${MOCK_ROOT}"
set +e
NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT}" \
NYXVEIL_VERSION=9.9.9 \
  run_mock_install "${TMP}/env.out"
rc=$?
set -e
if grep -q 'using NYXVEIL_VERSION from environment: 9.9.9' "${TMP}/env.out"; then
  pass "env override used 9.9.9"
else
  fail "env override did not log 9.9.9"
  cat "${TMP}/env.out" >&2 || true
fi
if [[ "${rc}" -ne 0 ]]; then
  pass "mock install exited non-zero as expected (fail-before-register)"
else
  fail "expected non-zero exit under NYXVEIL_INSTALL_FAIL_BEFORE_REGISTER=1"
fi

echo "== local VERSION file when env unset =="
MOCK_ROOT2="${TMP}/mock-local"
mkdir -p "${MOCK_ROOT2}"
set +e
(
  unset NYXVEIL_VERSION || true
  NYXVEIL_INSTALL_MOCK_ROOT="${MOCK_ROOT2}" \
    run_mock_install "${TMP}/local.out"
)
set -e
if grep -q 'using local candidate version from .*VERSION: 1.1.11' "${TMP}/local.out"; then
  pass "local VERSION file resolved to 1.1.11"
else
  fail "local VERSION resolution failed"
  cat "${TMP}/local.out" >&2 || true
fi

echo "== mocked curl stable resolution (no network) =="
MOCK_CURL_BIN="${TMP}/mockbin"
mkdir -p "${MOCK_CURL_BIN}"
cat >"${MOCK_CURL_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    --connect-timeout|--max-time) shift 2 ;;
    -H) shift 2 ;;
    -fsSL|-f|-s|-S|-L) shift ;;
    http*|HTTP*)
      payload='[{"tag_name":"server-v9.8.7","draft":false,"prerelease":false},{"tag_name":"server-v9.8.7-rc1","draft":false,"prerelease":true},{"tag_name":"other-v1.0.0","draft":false,"prerelease":false}]'
      if [[ -n "${out}" ]]; then
        printf '%s\n' "${payload}" >"${out}"
      else
        printf '%s\n' "${payload}"
      fi
      exit 0
      ;;
    *) shift ;;
  esac
done
echo "mock curl: no URL" >&2
exit 1
EOF
chmod +x "${MOCK_CURL_BIN}/curl"

set +e
resolved="$(
  PATH="${MOCK_CURL_BIN}:${PATH}" bash <<EOF
set -euo pipefail
GITHUB_REPO="Moroz1212/Nyxveil"
GITHUB_RELEASE_RESOLVE_TIMEOUT_SEC=30
die() { echo "\$*" >&2; exit 1; }
$(sed -n '/^resolve_stable_server_version()/,/^}/p' "${INSTALLER}")
resolve_stable_server_version
EOF
)"
rc=$?
set -e
if [[ "${rc}" -eq 0 && "${resolved}" == "9.8.7" ]]; then
  pass "mocked resolve_stable_server_version → 9.8.7"
else
  fail "mocked resolve_stable_server_version failed (got '${resolved:-}' rc=${rc})"
fi

echo "== semver MAX ignores GitHub API order + draft/prerelease =="
cat >"${MOCK_CURL_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    --connect-timeout|--max-time) shift 2 ;;
    -H) shift 2 ;;
    -fsSL|-f|-s|-S|-L) shift ;;
    http*|HTTP*)
      # Deliberately unordered: 1.1.9, 1.1.11, 1.1.10 + draft 1.1.12 + prerelease 1.1.13
      payload='[
        {"tag_name":"server-v1.1.9","draft":false,"prerelease":false},
        {"tag_name":"server-v1.1.11","draft":false,"prerelease":false},
        {"tag_name":"server-v1.1.10","draft":false,"prerelease":false},
        {"tag_name":"server-v1.1.12","draft":true,"prerelease":false},
        {"tag_name":"server-v1.1.13","draft":false,"prerelease":true}
      ]'
      if [[ -n "${out}" ]]; then
        printf '%s\n' "${payload}" >"${out}"
      else
        printf '%s\n' "${payload}"
      fi
      exit 0
      ;;
    *) shift ;;
  esac
done
echo "mock curl: no URL" >&2
exit 1
EOF
chmod +x "${MOCK_CURL_BIN}/curl"

set +e
resolved_max="$(
  PATH="${MOCK_CURL_BIN}:${PATH}" bash <<EOF
set -euo pipefail
GITHUB_REPO="Moroz1212/Nyxveil"
GITHUB_RELEASE_RESOLVE_TIMEOUT_SEC=30
die() { echo "\$*" >&2; exit 1; }
$(sed -n '/^resolve_stable_server_version()/,/^}/p' "${INSTALLER}")
resolve_stable_server_version
EOF
)"
rc=$?
set -e
if [[ "${rc}" -eq 0 && "${resolved_max}" == "1.1.11" ]]; then
  pass "semver MAX from unordered releases → 1.1.11"
else
  fail "semver MAX regression failed (got '${resolved_max:-}' rc=${rc}; want 1.1.11)"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-installer-version-resolution FAILED" >&2
  exit 1
fi
echo "test-installer-version-resolution PASSED"
