#!/usr/bin/env bash
# RELEASE_BYTES_IDENTITY: dist/release SHA256SUMS must match every UPLOAD-LIST file
# and manifests must reference those exact hashes (canonical CI bytes).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
UPLOAD_LIST="${DIST}/UPLOAD-LIST-server-v${VERSION}.txt"

die() { echo "assert-release-bytes-identity: $*" >&2; exit 1; }

[[ -d "${DIST}" ]] || die "missing ${DIST}"
[[ -f "${UPLOAD_LIST}" ]] || die "missing upload list"
[[ -f "${DIST}/SHA256SUMS" ]] || die "missing SHA256SUMS"

(
  cd "${DIST}"
  tr -d '\r' < SHA256SUMS | sha256sum -c - >/dev/null
)

while IFS= read -r name || [[ -n "${name}" ]]; do
  name="$(printf '%s' "${name}" | tr -d '\r')"
  [[ -n "${name}" ]] || continue
  [[ -f "${DIST}/${name}" ]] || die "missing ${name}"
done < "${UPLOAD_LIST}"

go run "${ROOT}/scripts/verify-manifest-hashes.go" -dist "${DIST}" -version "${VERSION}" >/dev/null

# Packaged installer must be pinned to this VERSION.
grep -q "^DEFAULT_RELEASE_VERSION=\"${VERSION}\"" "${DIST}/install.sh" \
  || die "install.sh not pinned to ${VERSION}"

echo "RELEASE_BYTES_IDENTITY=PASS"
