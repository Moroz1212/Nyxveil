#!/usr/bin/env bash
# Assemble dist/ layout for offline install and GitHub Releases.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

VERSION="$(tr -d '[:space:]' < VERSION)"
BIN_SRC="${ROOT}/dist/bin"
DIST="${ROOT}/dist/release"
TAG="server-v${VERSION}"

[[ -d "${BIN_SRC}" ]] || { echo "run scripts/build-release.sh first" >&2; exit 1; }

rm -rf "${DIST}"
mkdir -p "${DIST}"

package_arch() {
  local arch="$1"
  local dest="${DIST}/linux-${arch}"
  mkdir -p "${dest}/installer" "${dest}/systemd" "${dest}/firewall" "${dest}/scripts" "${dest}/docs"

  cp -a "${BIN_SRC}/nyxveil-server-linux-${arch}" "${dest}/nyxveil-server"
  cp -a "${BIN_SRC}/nyxveilctl-linux-${arch}" "${dest}/nyxveilctl"
  chmod 0755 "${dest}/nyxveil-server" "${dest}/nyxveilctl"

  cp -a "${ROOT}/installer/"*.sh "${dest}/installer/"
  cp -a "${ROOT}/systemd/nyxveil-server.service" "${dest}/systemd/"
  cp -a "${ROOT}/firewall/nftables-nyxveil.conf" "${dest}/firewall/"
  cp -a "${ROOT}/scripts/"*.sh "${dest}/scripts/"
  cp -a "${ROOT}/docs/"*.md "${dest}/docs/" 2>/dev/null || true
  cp -a "${ROOT}/README.md" "${dest}/" 2>/dev/null || true
  cp -a "${ROOT}/VERSION" "${dest}/"
  cp -a "${ROOT}/THIRD_PARTY_CORE.md" "${dest}/" 2>/dev/null || true

  chmod 0755 "${dest}/installer/"*.sh "${dest}/scripts/"*.sh

  # Flat release assets (GitHub Downloads)
  cp -a "${BIN_SRC}/nyxveil-server-linux-${arch}" "${DIST}/"
  cp -a "${BIN_SRC}/nyxveilctl-linux-${arch}" "${DIST}/"
}

package_arch amd64
package_arch arm64
cp -a "${BIN_SRC}/SHA256SUMS" "${DIST}/SHA256SUMS"

# Tarballs for offline --binary-dir
(
  cd "${DIST}"
  tar -czf "nyxveil-server-${VERSION}-linux-amd64.tar.gz" linux-amd64
  tar -czf "nyxveil-server-${VERSION}-linux-arm64.tar.gz" linux-arm64
)

cat > "${DIST}/NOTES.txt" <<EOF
Nyxveil server ${TAG}

Offline install (example amd64):
  tar -xzf nyxveil-server-${VERSION}-linux-amd64.tar.gz
  sudo ./linux-amd64/installer/install.sh --binary-dir ./linux-amd64 --skip-download

Interactive curl install (downloads release binaries):
  curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash
EOF

echo "Packaged ${TAG} in ${DIST}"
ls -la "${DIST}"
