#!/usr/bin/env bash
# Compare an existing GitHub Release's asset digests to local dist/release SHA256SUMS.
# Used by server-release.yml before --clobber.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
TAG="${1:?tag required}"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"

die() { echo "assert-existing-release-matches-dist: $*" >&2; exit 1; }

[[ -f "${DIST}/SHA256SUMS" ]] || die "missing SHA256SUMS"
command -v gh >/dev/null 2>&1 || die "gh required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum required"

TMP="$(mktemp -d /tmp/nyxveil-rel-cmp.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

# Download each asset named in SHA256SUMS and compare.
while read -r want name; do
  [[ -n "${want}" && -n "${name}" ]] || continue
  name="$(printf '%s' "${name}" | tr -d '\r')"
  want="$(printf '%s' "${want}" | tr -d '\r')"
  gh release download "${TAG}" -p "${name}" -D "${TMP}" --clobber >/dev/null 2>&1 \
    || die "cannot download ${name} from ${TAG}"
  got="$(sha256sum "${TMP}/${name}" | awk '{print $1}')"
  [[ "${got}" == "${want}" ]] || die "hash mismatch for ${name}: release=${got} dist=${want}"
done < <(tr -d '\r' < "${DIST}/SHA256SUMS" | awk 'NF>=2 {print $1, $2}')

echo "EXISTING_RELEASE_MATCHES_DIST=PASS version=${VERSION} tag=${TAG}"
