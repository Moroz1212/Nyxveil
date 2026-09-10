#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
eval "$(sed -n '/^install_nftables()/,/^# Embedded units/p' "${ROOT}/installer/install.sh")"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
NFT_FILE="${TMP}/rules"
TLS_DOMAIN=example.test TLS_PORT=443 QUIC_PORT=443 VPN_SUBNET=10.66.0.0/24 MOCK=0 INSTALLED_NFT=0
log() { :; }
die() { exit 1; }
printf 'previous working rules\n' >"${NFT_FILE}"
cp "${NFT_FILE}" "${TMP}/before"
nft_cmd() { printf '%s\n' "$*" >>"${TMP}/commands"; return 1; }
if (install_nftables); then echo 'invalid rules accepted'; exit 1; fi
cmp "${TMP}/before" "${NFT_FILE}"
[[ "$(wc -l <"${TMP}/commands")" -eq 1 ]]
grep -q '^--check -f ' "${TMP}/commands"
nft_cmd() { printf '%s\n' "$*" >>"${TMP}/commands"; }
install_nftables
grep -q '^destroy table inet nyxveil' "${NFT_FILE}"
if grep -q '^delete\|^flush' "${TMP}/commands"; then exit 1; fi
[[ ! -f "${NFT_FILE}.next" ]]
echo 'FIREWALL_TRANSACTION=PASS (validation failure preserves file; atomic apply contract)'
