#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ "${1:-}" == --help ]]; then
  cat "${ROOT}/LOCAL-CANDIDATE.txt"
  exit 0
fi
[[ "$(uname -s)" == Linux ]] || { echo 'requires disposable Ubuntu 24.04; live gate NOT EXECUTED'; exit 2; }
for arg in "$@"; do
  case "${arg}" in --mode|--expected-version|--binary-dir|--installer|--test-self-signed)
    echo "bundle controls ${arg}; refusing override" >&2; exit 2 ;;
  esac
done
case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) exit 2 ;; esac
cd "${ROOT}"
sha256sum --strict -c BUNDLE-SHA256SUMS >/dev/null
version="$(tr -d '[:space:]' < VERSION)"
[[ "${version}" == 1.1.17 ]] || exit 1
exec bash "${ROOT}/linux-${arch}/scripts/clean-host-install-gate.sh" \
  --mode local-candidate --expected-version "${version}" \
  --installer "${ROOT}/install.sh" --binary-dir "${ROOT}/linux-${arch}" "$@"
