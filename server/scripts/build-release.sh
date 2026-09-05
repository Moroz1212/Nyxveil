#!/usr/bin/env bash
# Cross-compile nyxveil-server and nyxveilctl for linux-amd64 and linux-arm64.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

VERSION="$(tr -d '[:space:]' < VERSION)"
OUT="${ROOT}/dist/bin"
mkdir -p "${OUT}"

LDFLAGS="-s -w -X github.com/nyxveil/server/internal/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X github.com/nyxveil/server/internal/version.Built=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

build_one() {
  local goarch="$1"
  local suffix="linux-${goarch}"
  echo "==> building ${suffix}"
  CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${LDFLAGS}" \
    -o "${OUT}/nyxveil-server-${suffix}" ./cmd/nyxveil-server
  CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${LDFLAGS}" \
    -o "${OUT}/nyxveilctl-${suffix}" ./cmd/nyxveilctl
}

build_one amd64
build_one arm64

(
  cd "${OUT}"
  sha256sum nyxveil-server-linux-amd64 nyxveilctl-linux-amd64 \
            nyxveil-server-linux-arm64 nyxveilctl-linux-arm64 > SHA256SUMS
)

echo "Built ${VERSION} artifacts in ${OUT}"
cat "${OUT}/SHA256SUMS"
