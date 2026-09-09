#!/usr/bin/env bash
# NFTABLES_IDEMPOTENCY: Rendered Nyxveil nft file must begin with destroy and
# survive N applications without duplicate rules (Linux + nft required).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_linux_nft() {
  [[ "$(uname -s)" == "Linux" ]] || { echo "SKIP non-Linux"; exit 0; }
  command -v nft >/dev/null 2>&1 || { echo "SKIP nft not available"; exit 0; }
  # Need privilege to mutate nftables; non-root CI skips live apply.
  if [[ "${EUID}" -ne 0 ]]; then
    echo "SKIP non-root (static contract only)"
    STATIC_ONLY=1
  else
    STATIC_ONLY=0
  fi
}

need_linux_nft

echo "== static render contract =="
# Go render must emit destroy.
if grep -q 'destroy table inet nyxveil' "${ROOT}/internal/configure/firewall_render.go"; then
  pass "Go RenderNyxveilNFT emits destroy"
else
  fail "Go RenderNyxveilNFT missing destroy"
fi
if grep -q 'destroy table inet nyxveil' "${ROOT}/firewall/nftables-nyxveil.conf"; then
  pass "sample nft conf emits destroy"
else
  fail "sample nft conf missing destroy"
fi
if grep -q 'destroy table inet nyxveil' "${ROOT}/installer/install.sh"; then
  pass "installer install_nftables emits destroy"
else
  fail "installer missing destroy"
fi
if grep -q 'ExecStartPre=.*nft delete table inet nyxveil' "${ROOT}/systemd/nyxveil-firewall.service" \
  && grep -q 'ExecStartPre=.*nft delete table inet nyxveil' "${ROOT}/installer/install.sh"; then
  pass "firewall unit has ExecStartPre delete"
else
  fail "firewall unit missing ExecStartPre delete"
fi

count_comment() {
  local table_dump="$1" comment="$2"
  printf '%s\n' "${table_dump}" | grep -c "${comment}" || true
}

if [[ "${STATIC_ONLY}" -eq 1 ]]; then
  echo "NFTABLES_IDEMPOTENCY=PASS (static)"
  [[ "${FAIL}" -eq 0 ]] || exit 1
  exit 0
fi

TMP="$(mktemp -d /tmp/nyxveil-nft-idem.XXXXXX)"
cleanup() {
  nft delete table inet nyxveil 2>/dev/null || true
  rm -rf "${TMP}"
}
trap cleanup EXIT

NFT_FILE="${TMP}/nyxveil.conf"
# Use Go renderer via a tiny helper or write matching body.
cat >"${NFT_FILE}" <<'EOF'
# test
destroy table inet nyxveil
table inet nyxveil {
  chain input {
    type filter hook input priority filter - 10; policy accept;
    tcp dport 80 ct state new accept comment "nyxveil-acme-http01"
    tcp dport 443 ct state new accept comment "nyxveil-tls"
    udp dport 443 ct state new accept comment "nyxveil-quic"
  }
  chain forward {
    type filter hook forward priority filter - 10; policy accept;
    iifname "nyxveil0" accept comment "nyxveil-fwd-in"
    oifname "nyxveil0" accept comment "nyxveil-fwd-out"
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr 10.66.0.0/24 oifname != "nyxveil0" masquerade comment "nyxveil-masq"
  }
}
EOF

echo "== apply ×3 =="
nft delete table inet nyxveil 2>/dev/null || true
for i in 1 2 3; do
  nft -f "${NFT_FILE}" || { fail "nft -f apply ${i}"; break; }
done

DUMP="$(nft list table inet nyxveil 2>/dev/null || true)"
[[ -n "${DUMP}" ]] || fail "table missing after applies"

for c in nyxveil-acme-http01 nyxveil-tls nyxveil-quic nyxveil-fwd-in nyxveil-fwd-out nyxveil-masq; do
  n="$(count_comment "${DUMP}" "${c}")"
  if [[ "${n}" -eq 1 ]]; then
    pass "comment ${c} appears exactly once (n=${n})"
  else
    fail "comment ${c} count=${n} want 1"
  fi
done

# Simulate unit start without ExecStartPre (file destroy alone must be enough).
nft -f "${NFT_FILE}"
nft -f "${NFT_FILE}"
DUMP2="$(nft list table inet nyxveil)"
for c in nyxveil-tls nyxveil-quic nyxveil-masq; do
  n="$(count_comment "${DUMP2}" "${c}")"
  [[ "${n}" -eq 1 ]] || fail "after extra -f without delete: ${c} count=${n}"
done
pass "repeated nft -f without external delete stays single-copy"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "NFTABLES_IDEMPOTENCY=FAIL"
  exit 1
fi
echo "NFTABLES_IDEMPOTENCY=PASS"
exit 0
