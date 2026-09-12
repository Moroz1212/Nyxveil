#!/usr/bin/env bash
# PINNED_RELEASE_INSTALLER: packaged install.sh with DEFAULT_RELEASE_VERSION must
# request server-v${VERSION} even when GitHub "latest" would resolve to a newer tag.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0
PRODUCT_VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
INSTALLER_SRC="${ROOT}/installer/install.sh"

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need_cmd bash

[[ -f "${INSTALLER_SRC}" ]] || { echo "missing installer" >&2; exit 1; }
grep -q '^DEFAULT_RELEASE_VERSION=""' "${INSTALLER_SRC}" || fail "source installer missing empty DEFAULT_RELEASE_VERSION pin site"

TMP="$(mktemp -d /tmp/nyxveil-pinned-installer.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

# Simulate package-release pin embedding.
PINNED="${TMP}/install.sh"
cp -a "${INSTALLER_SRC}" "${PINNED}"
sed -i.bak "s/^DEFAULT_RELEASE_VERSION=\"\"/DEFAULT_RELEASE_VERSION=\"${PRODUCT_VERSION}\"/" "${PINNED}"
rm -f "${PINNED}.bak"
grep -q "^DEFAULT_RELEASE_VERSION=\"${PRODUCT_VERSION}\"" "${PINNED}" \
  || fail "failed to embed DEFAULT_RELEASE_VERSION"

# Mock resolve_stable_server_version to prove it is NOT used when pin is set.
# We intercept by wrapping: extract resolve_installer_version path via dry-run logging.
# Installer logs either DEFAULT_RELEASE_VERSION or resolve latest.

# Stub http_get / GitHub so if pin is ignored, latest would be 9.9.9.
cat >"${TMP}/gh_latest.json" <<'EOF'
{"tag_name":"server-v9.9.9"}
EOF

# Source only the version-resolution functions with a mock environment.
# Prefer end-to-end: NYXVEIL_INSTALL_MOCK with FAIL_BEFORE_REGISTER and inspect log.
BIN_DIR="${TMP}/bins"
mkdir -p "${BIN_DIR}/scripts"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/nyxveil-server"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/nyxveilctl"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/nyxveil-catalog-verify"
printf '#!/bin/sh\necho mock\n' > "${BIN_DIR}/scripts/production-gate.sh"
# Intentionally NO VERSION file in BIN_DIR — remote path uses DEFAULT_RELEASE_VERSION.
printf '# mock frozen\n7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n' \
  > "${BIN_DIR}/THIRD_PARTY_CORE.md"
chmod +x "${BIN_DIR}/nyxveil-server" "${BIN_DIR}/nyxveilctl" \
  "${BIN_DIR}/nyxveil-catalog-verify" "${BIN_DIR}/scripts/production-gate.sh"

CA_FILE="${TMP}/cp-ca.pem"
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'MIIB' '-----END CERTIFICATE-----' > "${CA_FILE}"

# Unit-test resolve_installer_version by sourcing a snippet.
cat >"${TMP}/probe.sh" <<EOF
set -euo pipefail
DEFAULT_RELEASE_VERSION="${PRODUCT_VERSION}"
NYXVEIL_VERSION_ENV_OVERRIDE=""
NYXVEIL_VERSION=""
BINARY_DIR=""
SKIP_DOWNLOAD=0
log() { echo "LOG: \$*"; }
die() { echo "DIE: \$*" >&2; exit 1; }
resolve_stable_server_version() {
  echo "9.9.9"
}
$(sed -n '/^resolve_installer_version()/,/^}/p' "${PINNED}")
resolve_installer_version
echo "RESOLVED=\${NYXVEIL_VERSION}"
EOF

OUT="$(bash "${TMP}/probe.sh" 2>&1 || true)"
echo "${OUT}"
if echo "${OUT}" | grep -q "RESOLVED=${PRODUCT_VERSION}"; then
  pass "DEFAULT_RELEASE_VERSION wins over mock latest 9.9.9"
else
  fail "expected RESOLVED=${PRODUCT_VERSION}; got: ${OUT}"
fi
if echo "${OUT}" | grep -q "RESOLVED=9.9.9"; then
  fail "floated to latest 9.9.9"
fi

# Env override still wins over pin.
cat >"${TMP}/probe-env.sh" <<EOF
set -euo pipefail
DEFAULT_RELEASE_VERSION="${PRODUCT_VERSION}"
NYXVEIL_VERSION_ENV_OVERRIDE="2.0.0"
NYXVEIL_VERSION=""
BINARY_DIR=""
SKIP_DOWNLOAD=0
log() { echo "LOG: \$*"; }
die() { echo "DIE: \$*" >&2; exit 1; }
resolve_stable_server_version() { echo "9.9.9"; }
$(sed -n '/^resolve_installer_version()/,/^}/p' "${PINNED}")
resolve_installer_version
echo "RESOLVED=\${NYXVEIL_VERSION}"
EOF
OUT2="$(bash "${TMP}/probe-env.sh" 2>&1 || true)"
if echo "${OUT2}" | grep -q "RESOLVED=2.0.0"; then
  pass "NYXVEIL_VERSION env override still wins"
else
  fail "env override failed: ${OUT2}"
fi

# Generic (unpinned) source still floats when no env/local.
cat >"${TMP}/probe-float.sh" <<EOF
set -euo pipefail
DEFAULT_RELEASE_VERSION=""
NYXVEIL_VERSION_ENV_OVERRIDE=""
NYXVEIL_VERSION=""
BINARY_DIR=""
SKIP_DOWNLOAD=0
log() { echo "LOG: \$*"; }
die() { echo "DIE: \$*" >&2; exit 1; }
resolve_stable_server_version() { echo "9.9.9"; }
$(sed -n '/^resolve_installer_version()/,/^}/p' "${INSTALLER_SRC}")
resolve_installer_version
echo "RESOLVED=\${NYXVEIL_VERSION}"
EOF
OUT3="$(bash "${TMP}/probe-float.sh" 2>&1 || true)"
if echo "${OUT3}" | grep -q "RESOLVED=9.9.9"; then
  pass "generic installer still resolves latest when unpinned"
else
  fail "generic float broken: ${OUT3}"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "PINNED_RELEASE_INSTALLER=FAIL"
  exit 1
fi
echo "PINNED_RELEASE_INSTALLER=PASS"
exit 0
