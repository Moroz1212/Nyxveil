#!/usr/bin/env bash
# Manifest shell↔Go interop + curl|bash BASH_SOURCE regression.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
FAIL=0
pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

AMD64="dist/release/release-manifest-linux-amd64.json"
ARM64="dist/release/release-manifest-linux-arm64.json"
INSTALLER="installer/install.sh"
WANT_AMD64_SHA="e4a4fcb21b4bcffbf6c08b28b757dc8f7a5b0f30c66d8a961c3a7960f5128261"
RELEASE_BASE="https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.0.0"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need bash
need jq
need openssl
need curl

ensure_manifests() {
  mkdir -p dist/release
  if [[ ! -f "${AMD64}" ]]; then
    echo "fetching published ${AMD64} from server-v1.0.0…"
    curl -fsSL -o "${AMD64}" "${RELEASE_BASE}/release-manifest-linux-amd64.json"
  fi
  if [[ ! -f "${ARM64}" ]]; then
    echo "fetching published ${ARM64} from server-v1.0.0…"
    curl -fsSL -o "${ARM64}" "${RELEASE_BASE}/release-manifest-linux-arm64.json"
  fi
}

ensure_manifests

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
  out="$(mktemp)"
  bash "${INSTALLER}" --dump-canonical "${man}" > "${out}"
  len="$(wc -c < "${out}" | tr -d ' ')"
  last="$(tail -c 1 "${out}" | od -An -tx1 | tr -d ' \n')"
  if [[ "${last}" == "0a" ]]; then
    fail "TRAILING NEWLINE present in canonical for ${man}"
  else
    pass "no trailing LF for $(basename "${man}") (len=${len} last=0x${last})"
  fi
  rm -f "${out}"
done

echo "== Go ParseManifest + shell↔Go byte equality =="
if go test -timeout 60s -run 'TestProductionManifestsParseAndMatchKnownAMD64SHA|TestShellCanonicalBytesMatchGo|TestShellVerifyProductionManifests' ./internal/updater/; then
  pass "SHELL ↔ GO CANONICAL BYTES / Go ParseManifest"
else
  fail "SHELL ↔ GO CANONICAL BYTES"
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
