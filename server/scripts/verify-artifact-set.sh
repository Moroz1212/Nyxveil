#!/usr/bin/env bash
# CI_UPLOAD_SET_COMPLETE: fail closed when UPLOAD-LIST entries are missing from
# the canonical release staging directory that will be uploaded as the CI artifact.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
UPLOAD_LIST="${DIST}/UPLOAD-LIST-server-v${VERSION}.txt"

die() { echo "verify-artifact-set: $*" >&2; exit 1; }

[[ -d "${DIST}" ]] || die "missing ${DIST}"
[[ -f "${UPLOAD_LIST}" ]] || die "missing $(basename "${UPLOAD_LIST}")"

MISSING=0
while IFS= read -r name || [[ -n "${name}" ]]; do
  name="$(printf '%s' "${name}" | tr -d '\r')"
  [[ -n "${name}" ]] || continue
  if [[ ! -f "${DIST}/${name}" ]]; then
    echo "MISSING: ${name}" >&2
    MISSING=1
  fi
done < "${UPLOAD_LIST}"
[[ "${MISSING}" -eq 0 ]] || die "UPLOAD-LIST entries missing from ${DIST}"

# Re-run upload-set integrity (hashes + required management assets).
bash "${ROOT}/scripts/check-release-upload-set.sh"

# Prove pin site in packaged installer.
grep -q "^DEFAULT_RELEASE_VERSION=\"${VERSION}\"" "${DIST}/install.sh" \
  || die "packaged install.sh missing DEFAULT_RELEASE_VERSION=${VERSION}"

echo "CI_UPLOAD_SET_COMPLETE=PASS"
echo "MANIFEST_ASSETS_PRESENT=PASS"
echo "PINNED_INSTALLER_EMBED=PASS"
