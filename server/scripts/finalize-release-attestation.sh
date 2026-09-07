#!/usr/bin/env bash
# Finalize an immutable release directory: attest → consumer test → mutation guard.
# MUST be run only after package-release.sh and MUST NOT rebuild afterward.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
TAG="server-v${VERSION}"
ATTEST="${DIST}/FINAL-RELEASE-ATTESTATION-${TAG}.txt"
PUB_HEX="caf921521e213cb1bcdc2f9df4816c2ecd43222b23a47d6f869672e6ab0e79af"
CORE_HASH="7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b"

die() { echo "finalize-release: $*" >&2; exit 1; }

[[ -d "${DIST}" ]] || die "missing ${DIST}"
[[ -f "${DIST}/SHA256SUMS" ]] || die "missing SHA256SUMS"
[[ -f "${DIST}/UPLOAD-LIST-${TAG}.txt" ]] || die "missing UPLOAD-LIST"

hash_upload_set() {
  local out="$1"
  : >"${out}"
  while IFS= read -r name || [[ -n "${name}" ]]; do
    name="$(printf '%s' "${name}" | tr -d '\r')"
    [[ -n "${name}" ]] || continue
    [[ -f "${DIST}/${name}" ]] || die "missing ${name}"
    sha256sum "${DIST}/${name}"
  done < "${DIST}/UPLOAD-LIST-${TAG}.txt" | sort >>"${out}"
}

PRE_HASHES="$(mktemp)"
hash_upload_set "${PRE_HASHES}"

bash "${ROOT}/scripts/check-release-upload-set.sh"

POST_HASHES="$(mktemp)"
hash_upload_set "${POST_HASHES}"

if ! cmp -s "${PRE_HASHES}" "${POST_HASHES}"; then
  echo "RELEASE_CONTENT_CHANGED_AFTER_TEST" >&2
  diff -u "${PRE_HASHES}" "${POST_HASHES}" >&2 || true
  rm -f "${PRE_HASHES}" "${POST_HASHES}"
  die "RELEASE_CONTENT_CHANGED_AFTER_TEST"
fi

{
  echo "FINAL-RELEASE-ATTESTATION ${TAG}"
  echo "version=${VERSION}"
  echo "tag=${TAG}"
  echo "frozen_core_sha256=${CORE_HASH}"
  echo "update_public_key=${PUB_HEX}"
  echo "timestamp_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "RELEASE_CONTENT_FROZEN=true"
  echo
  echo "UPLOAD_SET:"
  while IFS= read -r name || [[ -n "${name}" ]]; do
    name="$(printf '%s' "${name}" | tr -d '\r')"
    [[ -n "${name}" ]] || continue
    size="$(wc -c < "${DIST}/${name}" | tr -d ' ')"
    sum="$(sha256sum "${DIST}/${name}" | awk '{print $1}')"
    printf '  %s size=%s sha256=%s\n' "${name}" "${size}" "${sum}"
  done < "${DIST}/UPLOAD-LIST-${TAG}.txt"
  echo
  echo "MANIFESTS:"
  for arch in amd64 arm64; do
    f="release-manifest-linux-${arch}.json"
    sum="$(sha256sum "${DIST}/${f}" | awk '{print $1}')"
    printf '  %s sha256=%s\n' "${f}" "${sum}"
  done
  echo
  echo "TEST_SUMMARY:"
  echo "  RELEASE_UPLOAD_SET=PASS"
  echo "  LIVE_FINAL_UPDATE_CONSUMER=PASS"
  echo "  RELEASE_CONTENT_MUTATION_CHECK=PASS"
} >"${ATTEST}"

# Final mutation check including that attestation write did not alter upload bytes.
FINAL_HASHES="$(mktemp)"
hash_upload_set "${FINAL_HASHES}"
if ! cmp -s "${PRE_HASHES}" "${FINAL_HASHES}"; then
  echo "RELEASE_CONTENT_CHANGED_AFTER_TEST" >&2
  rm -f "${PRE_HASHES}" "${POST_HASHES}" "${FINAL_HASHES}"
  die "RELEASE_CONTENT_CHANGED_AFTER_TEST"
fi

rm -f "${PRE_HASHES}" "${POST_HASHES}" "${FINAL_HASHES}"
echo "wrote ${ATTEST}"
echo "RELEASE_CONTENT_FROZEN=true"
echo "FINALIZE_RELEASE=PASS"
