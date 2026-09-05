#!/usr/bin/env bash
# verify-release.sh — fail if release tree has stale/mismatched hashes or
# competing legacy manifest formats.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
BIN="${ROOT}/dist/bin"
VERSION="$(tr -d '[:space:]' < "${ROOT}/VERSION")"

die() { echo "verify-release: $*" >&2; exit 1; }

echo "==> Frozen Core gate"
bash "${ROOT}/scripts/assert-frozen-core.sh"

echo "==> Release tree present"
[[ -d "${DIST}" ]] || die "missing ${DIST} (run build-release + package-release)"
[[ -d "${BIN}" ]] || die "missing ${BIN}"

[[ ! -f "${ROOT}/dist/release-manifest.json" ]] || die "legacy dist/release-manifest.json must not exist"
[[ ! -d "${ROOT}/dist/checksums" ]] || die "stale dist/checksums must not exist"
[[ ! -d "${ROOT}/dist/linux-amd64" ]] || die "stale dist/linux-amd64 must not exist (use dist/release/)"
[[ ! -d "${ROOT}/dist/linux-arm64" ]] || die "stale dist/linux-arm64 must not exist"

REQUIRED=(
  nyxveil-server-linux-amd64
  nyxveilctl-linux-amd64
  nyxveil-server-linux-arm64
  nyxveilctl-linux-arm64
  release-manifest-linux-amd64.json
  release-manifest-linux-arm64.json
  SHA256SUMS
)

HASHED=(
  nyxveil-server-linux-amd64
  nyxveilctl-linux-amd64
  nyxveil-server-linux-arm64
  nyxveilctl-linux-arm64
  release-manifest-linux-amd64.json
  release-manifest-linux-arm64.json
)

echo "==> Required assets"
for f in "${REQUIRED[@]}"; do
  [[ -f "${DIST}/${f}" ]] || die "missing ${DIST}/${f}"
done

echo "==> SHA256SUMS matches every listed file and includes required assets"
(
  cd "${DIST}"
  while read -r hash name; do
    [[ -n "${hash:-}" ]] || continue
    [[ "${hash}" =~ ^[0-9a-fA-F]{64}$ ]] || continue
    name="${name#\*}"
    name="$(basename "${name}")"
    [[ -f "${name}" ]] || die "SHA256SUMS lists missing file ${name}"
    got="$(sha256sum "${name}" | awk '{print $1}')"
    [[ "${got}" == "${hash}" ]] || die "hash mismatch for ${name}: sums=${hash} file=${got}"
  done < SHA256SUMS
  for f in "${HASHED[@]}"; do
    grep -E -q "[[:space:]]\\*?${f}\$" SHA256SUMS || die "SHA256SUMS missing entry for ${f}"
  done
)

echo "==> Staging bin hashes match release flat assets"
for f in nyxveil-server-linux-amd64 nyxveilctl-linux-amd64 nyxveil-server-linux-arm64 nyxveilctl-linux-arm64; do
  a="$(sha256sum "${BIN}/${f}" | awk '{print $1}')"
  b="$(sha256sum "${DIST}/${f}" | awk '{print $1}')"
  [[ "${a}" == "${b}" ]] || die "bin/release mismatch for ${f}"
done

echo "==> Manifest asset hashes match release binaries (signed ParseManifest)"
go run ./scripts/verify-manifest-hashes.go -dist "${DIST}" -version "${VERSION}"

echo "==> Version consistency"
if ! grep -q "\"${VERSION}\"" "${DIST}/release-manifest-linux-amd64.json"; then
  die "amd64 manifest version != VERSION (${VERSION})"
fi

echo "verify-release: OK version=${VERSION}"
