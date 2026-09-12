#!/usr/bin/env bash
# An existing release must contain exactly the canonical upload set and bytes.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
TAG="${1:?tag required}"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
[[ "${TAG}" == "server-v${VERSION}" ]] || exit 1
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
LIST="${DIST}/UPLOAD-LIST-server-v${VERSION}.txt"
tr -d '\r' <"${LIST}" | LC_ALL=C sort >"${TMP}/expected"
gh release view "${TAG}" --json assets --jq '.assets[].name' | LC_ALL=C sort >"${TMP}/actual"
cmp "${TMP}/expected" "${TMP}/actual"
gh release download "${TAG}" -D "${TMP}/assets"
while IFS= read -r name; do
  [[ -n "${name}" && "${name}" == "$(basename "${name}")" && "${name}" != . && "${name}" != .. ]] || exit 1
  cmp "${DIST}/${name}" "${TMP}/assets/${name}"
done <"${TMP}/expected"
echo "EXISTING_RELEASE_MATCHES_DIST=PASS version=${VERSION} tag=${TAG}"
