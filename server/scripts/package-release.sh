#!/usr/bin/env bash
# Assemble dist/ layout for offline install and GitHub Releases.
# Asset names align with installer/download + updater manifests:
#   nyxveil-server-linux-{amd64,arm64}
#   nyxveilctl-linux-{amd64,arm64}
#   release-manifest-linux-{amd64,arm64}.json
#   SHA256SUMS (secondary)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

bash "${ROOT}/scripts/assert-frozen-core.sh"

VERSION="$(tr -d '[:space:]' < VERSION)"
BIN_SRC="${ROOT}/dist/bin"
DIST="${ROOT}/dist/release"
TAG="server-v${VERSION}"
BASE_URL="${NYXVEIL_RELEASE_BASE_URL:-https://github.com/Moroz1212/Nyxveil/releases/download/${TAG}}"
REQUIRED_UPLOADS=(
  nyxveil-server-linux-amd64
  nyxveilctl-linux-amd64
  nyxveil-catalog-verify-linux-amd64
  nyxveil-server-linux-arm64
  nyxveilctl-linux-arm64
  nyxveil-catalog-verify-linux-arm64
  production-gate.sh
  VERSION
  THIRD_PARTY_CORE.md
  release-manifest-linux-amd64.json
  release-manifest-linux-arm64.json
  bootstrap-cli-update.sh
  live-final-update.sh
  SHA256SUMS
  "UPLOAD-LIST-server-v${VERSION}.txt"
)
HASHED_UPLOADS=(
  nyxveil-server-linux-amd64
  nyxveilctl-linux-amd64
  nyxveil-catalog-verify-linux-amd64
  nyxveil-server-linux-arm64
  nyxveilctl-linux-arm64
  nyxveil-catalog-verify-linux-arm64
  production-gate.sh
  VERSION
  THIRD_PARTY_CORE.md
  release-manifest-linux-amd64.json
  release-manifest-linux-arm64.json
  bootstrap-cli-update.sh
  live-final-update.sh
)

[[ -d "${BIN_SRC}" ]] || { echo "run scripts/build-release.sh first" >&2; exit 1; }
# Reject leftover legacy contracts from older packaging.
rm -f "${ROOT}/dist/release-manifest.json"
rm -rf "${ROOT}/dist/checksums" "${ROOT}/dist/linux-amd64" "${ROOT}/dist/linux-arm64"

rm -rf "${DIST}"
mkdir -p "${DIST}"

package_arch() {
  local arch="$1"
  local dest="${DIST}/linux-${arch}"
  mkdir -p "${dest}/installer" "${dest}/systemd" "${dest}/firewall" "${dest}/scripts" "${dest}/docs"

  cp -a "${BIN_SRC}/nyxveil-server-linux-${arch}" "${dest}/nyxveil-server"
  cp -a "${BIN_SRC}/nyxveilctl-linux-${arch}" "${dest}/nyxveilctl"
  cp -a "${BIN_SRC}/nyxveil-catalog-verify-linux-${arch}" "${dest}/nyxveil-catalog-verify"
  chmod 0755 "${dest}/nyxveil-server" "${dest}/nyxveilctl" "${dest}/nyxveil-catalog-verify"

  cp -a "${ROOT}/installer/"*.sh "${dest}/installer/"
  cp -a "${ROOT}/systemd/nyxveil-server.service" "${dest}/systemd/"
  cp -a "${ROOT}/systemd/nyxveil-firewall.service" "${dest}/systemd/"
  cp -a "${ROOT}/firewall/nftables-nyxveil.conf" "${dest}/firewall/"
  cp -a "${ROOT}/scripts/"*.sh "${dest}/scripts/"
  # Do not ship sign-release private-key tooling secrets; Go helper is fine to include for rebuilds.
  if [[ -f "${ROOT}/scripts/sign-release.go" ]]; then
    cp -a "${ROOT}/scripts/sign-release.go" "${dest}/scripts/"
  fi
  if [[ -f "${ROOT}/scripts/verify-manifest-hashes.go" ]]; then
    cp -a "${ROOT}/scripts/verify-manifest-hashes.go" "${dest}/scripts/"
  fi
  if [[ -f "${ROOT}/scripts/assert-frozen-core.sh" ]]; then
    cp -a "${ROOT}/scripts/assert-frozen-core.sh" "${dest}/scripts/"
  fi
  if [[ -f "${ROOT}/scripts/verify-release.sh" ]]; then
    cp -a "${ROOT}/scripts/verify-release.sh" "${dest}/scripts/"
  fi
  cp -a "${ROOT}/docs/"*.md "${dest}/docs/" 2>/dev/null || true
  cp -a "${ROOT}/README.md" "${dest}/" 2>/dev/null || true
  cp -a "${ROOT}/VERSION" "${dest}/"
  cp -a "${ROOT}/THIRD_PARTY_CORE.md" "${dest}/" 2>/dev/null || true

  chmod 0755 "${dest}/installer/"*.sh "${dest}/scripts/"*.sh

  # Deterministic LF for every shell script shipped to Linux (Windows checkouts).
  bash "${ROOT}/scripts/normalize-shell-lf.sh" "${dest}/installer" "${dest}/scripts" \
    "${dest}/systemd" "${dest}/firewall"

  # Flat release assets (GitHub Downloads)
  cp -a "${BIN_SRC}/nyxveil-server-linux-${arch}" "${DIST}/"
  cp -a "${BIN_SRC}/nyxveilctl-linux-${arch}" "${DIST}/"
  cp -a "${BIN_SRC}/nyxveil-catalog-verify-linux-${arch}" "${DIST}/"
}

package_arch amd64
package_arch arm64

# Arch-independent auxiliary install/gate assets (hashed + signed into manifests).
bash "${ROOT}/scripts/normalize-shell-lf.sh" "${ROOT}/scripts/production-gate.sh"
cp -a "${ROOT}/scripts/production-gate.sh" "${DIST}/production-gate.sh"
chmod 0755 "${DIST}/production-gate.sh"
bash "${ROOT}/scripts/normalize-shell-lf.sh" "${DIST}/production-gate.sh"
cp -a "${ROOT}/VERSION" "${DIST}/VERSION"
cp -a "${ROOT}/THIRD_PARTY_CORE.md" "${DIST}/THIRD_PARTY_CORE.md"
chmod 0644 "${DIST}/VERSION" "${DIST}/THIRD_PARTY_CORE.md"

# CLI-first bootstrap and operator final-update wrapper.
bash "${ROOT}/scripts/normalize-shell-lf.sh" \
  "${ROOT}/scripts/bootstrap-cli-update.sh" \
  "${ROOT}/scripts/live-final-update.sh"
cp -a "${ROOT}/scripts/bootstrap-cli-update.sh" "${DIST}/bootstrap-cli-update.sh"
cp -a "${ROOT}/scripts/live-final-update.sh" "${DIST}/live-final-update.sh"
chmod 0755 "${DIST}/bootstrap-cli-update.sh" "${DIST}/live-final-update.sh"
bash "${ROOT}/scripts/normalize-shell-lf.sh" \
  "${DIST}/bootstrap-cli-update.sh" \
  "${DIST}/live-final-update.sh"
bash "${ROOT}/scripts/assert-no-crlf.sh" \
  "${DIST}/bootstrap-cli-update.sh" \
  "${DIST}/live-final-update.sh" \
  "${DIST}/linux-amd64/scripts" "${DIST}/linux-arm64/scripts" \
  "${DIST}/linux-amd64/installer" "${DIST}/linux-arm64/installer"

# Sign manifests (fail-closed unless SKIP_SIGN=1 for local unsigned experiments).
if [[ "${SKIP_SIGN:-0}" == "1" ]]; then
  echo "SKIP_SIGN=1 — not writing signed manifests" >&2
else
  go run ./scripts/sign-release.go \
    -version "${VERSION}" \
    -out "${DIST}" \
    -base-url "${BASE_URL}" \
    -amd64-server "${BIN_SRC}/nyxveil-server-linux-amd64" \
    -amd64-ctl "${BIN_SRC}/nyxveilctl-linux-amd64" \
    -amd64-catalog "${BIN_SRC}/nyxveil-catalog-verify-linux-amd64" \
    -arm64-server "${BIN_SRC}/nyxveil-server-linux-arm64" \
    -arm64-ctl "${BIN_SRC}/nyxveilctl-linux-arm64" \
    -arm64-catalog "${BIN_SRC}/nyxveil-catalog-verify-linux-arm64" \
    -production-gate "${DIST}/production-gate.sh" \
    -share-version "${DIST}/VERSION" \
    -share-third-party "${DIST}/THIRD_PARTY_CORE.md"
fi

# Exact flat files that must be uploaded for this server release. Keep this
# generated list adjacent to the assets so upload tooling cannot omit auxiliaries.
UPLOAD_LIST="${DIST}/UPLOAD-LIST-server-v${VERSION}.txt"
printf '%s\n' "${REQUIRED_UPLOADS[@]}" > "${UPLOAD_LIST}"
chmod 0644 "${UPLOAD_LIST}"

# Secondary checksums for humans / older tooling
(
  cd "${DIST}"
  sha256sum "${HASHED_UPLOADS[@]}" > SHA256SUMS
)

# Tarballs for offline --binary-dir
(
  cd "${DIST}"
  tar -czf "nyxveil-server-${VERSION}-linux-amd64.tar.gz" linux-amd64
  tar -czf "nyxveil-server-${VERSION}-linux-arm64.tar.gz" linux-arm64
)

cat > "${DIST}/NOTES.txt" <<EOF
Nyxveil server ${TAG}

Canonical release assets (exact names):
  nyxveil-server-linux-{amd64,arm64}
  nyxveilctl-linux-{amd64,arm64}
  nyxveil-catalog-verify-linux-{amd64,arm64}
  production-gate.sh
  VERSION
  THIRD_PARTY_CORE.md
  release-manifest-linux-{amd64,arm64}.json
  bootstrap-cli-update.sh
  live-final-update.sh
  SHA256SUMS

After update, production gate MUST exist at:
  /usr/local/share/nyxveil/scripts/production-gate.sh

Live final update (ONE command; verifies the wrapper before executing it):
  BASE=https://github.com/Moroz1212/Nyxveil/releases/download/server-v${VERSION}
  cd /tmp && curl -fsSLO "\$BASE/SHA256SUMS" "\$BASE/live-final-update.sh" && \\
  grep ' live-final-update.sh\$' SHA256SUMS | sha256sum -c - && \\
  chmod 0755 live-final-update.sh && \\
  sudo ./live-final-update.sh --base-url "\$BASE"

The verified live-final-update.sh verifies bootstrap-cli-update.sh, replaces only
nyxveilctl first, runs the complete updater, checks required auxiliaries, then
executes production-gate.sh. Do not run the old 1.1.1 updater first.

Offline install (example amd64):
  tar -xzf nyxveil-server-${VERSION}-linux-amd64.tar.gz
  sudo ./linux-amd64/installer/install.sh --binary-dir ./linux-amd64 --skip-download \\
    --control-plane https://example --location x --name y --public-host z --bootstrap-token "\$TOKEN"
EOF

echo "Packaged ${TAG} in ${DIST}"
ls -la "${DIST}"
if [[ "${SKIP_SIGN:-0}" == "1" ]]; then
  echo "SKIP_SIGN=1 — skipping verify-release (unsigned)"
else
  bash "${ROOT}/scripts/verify-release.sh"
  bash "${ROOT}/scripts/check-release-upload-set.sh"
fi
