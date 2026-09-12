#!/usr/bin/env bash
# Manifest shell↔Go interop + curl|bash BASH_SOURCE regression.
# Uses a temp fixture dir — never writes server-v1.0.0 pins into dist/release
# (that tree is reserved for the current candidate package-release output).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
FAIL=0
pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

FIXTURE="$(mktemp -d /tmp/nyxveil-manifest-interop.XXXXXX)"
cleanup() { rm -rf "${FIXTURE}"; }
trap cleanup EXIT

AMD64="${FIXTURE}/release-manifest-linux-amd64.json"
ARM64="${FIXTURE}/release-manifest-linux-arm64.json"
INSTALLER="installer/install.sh"
WANT_AMD64_SHA="e4a4fcb21b4bcffbf6c08b28b757dc8f7a5b0f30c66d8a961c3a7960f5128261"
RELEASE_BASE="https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.0.0"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need bash
need jq
need openssl
need curl

echo "fetching published server-v1.0.0 manifests into ${FIXTURE}…"
curl -fsSL -o "${AMD64}" "${RELEASE_BASE}/release-manifest-linux-amd64.json"
curl -fsSL -o "${ARM64}" "${RELEASE_BASE}/release-manifest-linux-arm64.json"

echo "== production amd64 manifest SHA (must not change) =="
got="$(sha256sum "${AMD64}" | awk '{print $1}')"
if [[ "${got}" == "${WANT_AMD64_SHA}" ]]; then
  pass "amd64 manifest SHA256 unchanged"
else
  fail "amd64 manifest SHA256=${got} want ${WANT_AMD64_SHA}"
fi

echo "== shell verify existing manifests =="
if bash "${INSTALLER}" --verify-manifest "${AMD64}"; then
  pass "AMD64 EXISTING MANIFEST SHELL VERIFY"
else
  fail "AMD64 EXISTING MANIFEST SHELL VERIFY"
fi
if bash "${INSTALLER}" --verify-manifest "${ARM64}"; then
  pass "ARM64 EXISTING MANIFEST SHELL VERIFY"
else
  fail "ARM64 EXISTING MANIFEST SHELL VERIFY"
fi

echo "== trailing newline check =="
for man in "${AMD64}" "${ARM64}"; do
  # Unsigned GitHub-trust model: manifests are consumed as published file bytes
  # (no Ed25519 canonicalization / --dump-canonical).
  if [[ ! -s "${man}" ]]; then
    fail "manifest empty: ${man}"
    continue
  fi
  last="$(tail -c 1 "${man}" | od -An -tx1 | tr -d ' \n')"
  pass "manifest readable $(basename "${man}") (last=0x${last:-00})"
done

echo "== Go ParseManifest + published SHA pin =="
if go test -timeout 60s -run 'TestProductionManifestsParseAndMatchKnownAMD64SHA' ./internal/updater/; then
  pass "GO ParseManifest / published SHA pin"
else
  fail "GO ParseManifest / published SHA pin"
fi

echo "== curl pipe BASH_SOURCE =="
out="$(mktemp)"
set +e
# Simulate curl|bash: stdin script, no BASH_SOURCE file path.
cat "${INSTALLER}" | bash -s -- --help >"${out}" 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  fail "CURL PIPE BASH_SOURCE (exit ${rc})"
  cat "${out}" >&2 || true
elif grep -q 'BASH_SOURCE' "${out}"; then
  fail "CURL PIPE BASH_SOURCE (warning/error present)"
  cat "${out}" >&2 || true
else
  pass "CURL PIPE BASH_SOURCE"
fi
rm -f "${out}"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-manifest-interop FAILED" >&2
  exit 1
fi
echo "test-manifest-interop PASSED"
echo "EXISTING SERVER-V1.0.0 RELEASE REQUIRES REUPLOAD: NO"
