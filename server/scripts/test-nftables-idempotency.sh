#!/usr/bin/env bash
# Apply the shipped configuration only in a private network namespace.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$(uname -s)" != Linux || ${EUID} -ne 0 ]]; then
  echo 'NFTABLES_IDEMPOTENCY=SKIP (requires root Linux)'
  exit 0
fi
command -v nft >/dev/null
command -v unshare >/dev/null
if [[ "${1:-}" != --inside-netns ]]; then
  exec unshare --net -- bash "${BASH_SOURCE[0]}" --inside-netns
fi
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
# Exercise the actual installer writer, with all destinations in this fixture.
eval "$(sed -n '/^install_nftables()/,/^# Embedded units/p' "${ROOT}/installer/install.sh")"
NFT_FILE="${TMP}/installer.conf"
TLS_DOMAIN=probe.example.test
TLS_PORT=443
QUIC_PORT=443
VPN_SUBNET=10.66.0.0/24
MOCK=0
nft_cmd() { nft "$@"; }
log() { :; }
nft add table inet unrelated
nft list table inet unrelated >"${TMP}/foreign"
for source in "${ROOT}/firewall/nftables-nyxveil.conf" "${NFT_FILE}"; do
  for iteration in 1 2 3 4 5 6; do
    if [[ "${source}" == "${NFT_FILE}" ]]; then
      install_nftables
    else
      nft -f "${source}"
    fi
    nft list table inet nyxveil >"${TMP}/current"
    if [[ ${iteration} -eq 1 ]]; then
      cp "${TMP}/current" "${TMP}/baseline"
    else
      cmp "${TMP}/baseline" "${TMP}/current"
    fi
    nft list table inet unrelated >"${TMP}/foreign-current"
    cmp "${TMP}/foreign" "${TMP}/foreign-current"
  done
  # A failed atomic replacement must preserve the installed rules.
  { cat "${source}"; echo 'this is deliberately invalid nft syntax'; } >"${TMP}/invalid"
  if nft -f "${TMP}/invalid" 2>"${TMP}/error"; then
    echo 'NFTABLES_IDEMPOTENCY=FAIL (invalid transaction accepted)'; exit 1
  fi
  nft list table inet nyxveil >"${TMP}/current"
  cmp "${TMP}/baseline" "${TMP}/current"
done
echo 'NFTABLES_IDEMPOTENCY=PASS (shipped config, six applies, atomic failure, isolated namespace)'
echo 'INSTALL_REPAIR_NFT_E2E=NOT_EXECUTED (requires clean-host gate)'
