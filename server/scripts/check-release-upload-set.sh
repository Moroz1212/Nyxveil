#!/usr/bin/env bash
# Fail closed when the flat release upload set is incomplete or inconsistent.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist/release"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION")"
UPLOAD_LIST="${DIST}/UPLOAD-LIST-server-v${VERSION}.txt"

die() { echo "check-release-upload-set: $*" >&2; exit 1; }

[[ -d "${DIST}" ]] || die "missing ${DIST}"
[[ -f "${UPLOAD_LIST}" ]] || die "missing $(basename "${UPLOAD_LIST}")"

while IFS= read -r name || [[ -n "${name}" ]]; do
  name="$(printf '%s' "${name}" | tr -d '\r')"
  [[ -n "${name}" ]] || continue
  [[ "${name}" == "$(basename "${name}")" ]] || die "upload entry must be a basename: ${name}"
  [[ -f "${DIST}/${name}" ]] || die "upload entry missing from release: ${name}"
done < "${UPLOAD_LIST}"

for arch in amd64 arm64; do
  [[ -f "${DIST}/nyxveil-server-linux-${arch}" ]] || die "missing server ${arch}"
  [[ -f "${DIST}/nyxveilctl-linux-${arch}" ]] || die "missing ctl ${arch}"
  [[ -f "${DIST}/nyxveil-catalog-verify-linux-${arch}" ]] || die "missing catalog verifier ${arch}"
  [[ -f "${DIST}/release-manifest-linux-${arch}.json" ]] || die "missing manifest ${arch}"
done

[[ -x "${DIST}/production-gate.sh" ]] || die "production-gate.sh is not executable"
[[ -f "${DIST}/nyxveil-update.service" ]] || die "nyxveil-update.service missing"
[[ -f "${DIST}/50-nyxveil-management.rules" ]] || die "50-nyxveil-management.rules missing"
[[ -x "${DIST}/bootstrap-cli-update.sh" ]] || die "bootstrap-cli-update.sh is missing or not executable"
[[ -x "${DIST}/live-final-update.sh" ]] || die "live-final-update.sh is missing or not executable"
bash -n "${DIST}/bootstrap-cli-update.sh"
bash -n "${DIST}/live-final-update.sh"
bash -n "${DIST}/production-gate.sh"

bash "${ROOT}/scripts/assert-no-crlf.sh" \
  "${DIST}/VERSION" \
  "${DIST}/SHA256SUMS" \
  "${UPLOAD_LIST}" \
  "${DIST}/bootstrap-cli-update.sh" \
  "${DIST}/live-final-update.sh" \
  "${DIST}/production-gate.sh" \
  "${DIST}/nyxveil-update.service" \
  "${DIST}/50-nyxveil-management.rules"

(
  cd "${DIST}"
  tr -d '\r' < SHA256SUMS | sha256sum -c - >/dev/null
)

# This helper parses both unsigned manifests with Go ParseManifest,
# checks all required names, and hashes each referenced flat release asset.
go run "${ROOT}/scripts/verify-manifest-hashes.go" -dist "${DIST}" -version "${VERSION}" >/dev/null

echo "RELEASE_UPLOAD_SET=PASS"

# Consumer test: empty-dir invariant + GitHub/SHA256 trust chain against ONLY dist/release.
go run "${ROOT}/scripts/verify-live-final-consumer.go" "${DIST}"
echo "LIVE_FINAL_UPDATE_CONSUMER=PASS"
