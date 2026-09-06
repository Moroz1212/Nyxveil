#!/usr/bin/env bash
# Nyxveil server production gate.
#
# GATE_MODE=
#   source — verify built release artifacts + Frozen Core (no install, no secrets)
#   local  — installed node local health (no mutating stop unless GATE_STOP_TEST=1)
#   live   — local + CP reachability + cp_connected (asks license token once for catalog crypto)
#
# Never prints/stores license tokens, private keys, or node.key contents.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${GATE_MODE:-source}"
CTL="${NYXVEIL_CTL:-}"
SERVER="${NYXVEIL_SERVER:-}"
STATE_DIR="${NYXVEIL_STATE_DIR:-/var/lib/nyxveil}"
CONFIG="${NYXVEIL_CONFIG:-/etc/nyxveil/server.json}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/nyxveil-production-gate.${STAMP}.XXXXXX")"
BUNDLE="${TMPDIR:-/tmp}/nyxveil-production-gate.${STAMP}.tar.gz"
FAILED_GATE=""
LICENSE_TOKEN=""

cleanup() {
  LICENSE_TOKEN=""
  unset LICENSE_TOKEN GATE_LICENSE_TOKEN || true
  rm -rf "${WORK}"
}
trap cleanup EXIT

sanitize() {
  sed -E \
    -e 's/([Aa]uthorization:[[:space:]]*(Bearer|Basic))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/g' \
    -e 's/((bootstrap_|license_|node_)?token|password|secret)(["=:[:space:]]+)[^",[:space:]]+/\1\3[REDACTED]/Ig' \
    -e 's/-----BEGIN ([A-Z ]*)PRIVATE KEY-----.*$/[REDACTED PRIVATE KEY]/g'
}

record() { printf '%s\n' "$*" >>"${WORK}/gate.log"; }

fail() {
  FAILED_GATE="$1"
  record "FAIL ${FAILED_GATE}: ${2:-no detail}"
  finalize
}

finalize() {
  if command -v journalctl >/dev/null 2>&1 && [[ "${MODE}" != "source" ]]; then
    journalctl -u nyxveil-server --since '-30 minutes' --no-pager 2>&1 |
      sanitize >"${WORK}/journal-sanitized.log" || true
  fi
  if command -v systemctl >/dev/null 2>&1 && [[ "${MODE}" != "source" ]]; then
    systemctl show nyxveil-server \
      -p ActiveState -p SubState -p MainPID -p TimeoutStopUSec 2>&1 |
      sanitize >"${WORK}/systemd-status.txt" || true
  fi
  tar -czf "${BUNDLE}" -C "${WORK}" . 2>/dev/null || true
  if [[ -n "${FAILED_GATE}" ]]; then
    printf 'RESULT=FAIL failed_gate=%s diagnostic_bundle=%s\n' "${FAILED_GATE}" "${BUNDLE}"
    exit 1
  fi
  printf 'RESULT=PASS\n'
  exit 0
}

ask_license_once() {
  LICENSE_TOKEN="${GATE_LICENSE_TOKEN:-}"
  if [[ -z "${LICENSE_TOKEN}" ]]; then
    [[ -t 0 ]] || fail "license_token" "interactive hidden token input required (or set GATE_LICENSE_TOKEN)"
    read -r -s -p "License token: " LICENSE_TOKEN
    printf '\n' >&2
  fi
  [[ -n "${LICENSE_TOKEN}" ]] || fail "license_token" "empty token"
  record "license_token=provided_in_memory"
}

case "${MODE}" in
  source|local|live) ;;
  *) fail "mode" "GATE_MODE must be source|local|live" ;;
esac

VERSION="$(tr -d '[:space:]' < "${ROOT}/VERSION" 2>/dev/null || true)"
[[ "${VERSION}" == "1.1.0" ]] || fail "version_file" "expected VERSION=1.1.0 got '${VERSION}'"

if [[ -x "${ROOT}/scripts/assert-frozen-core.sh" && -d "${ROOT}/third_party/nvp" ]]; then
  bash "${ROOT}/scripts/assert-frozen-core.sh" >"${WORK}/frozen-core.txt" 2>&1 ||
    fail "frozen_core" "source-tree assertion failed"
else
  grep -q '7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b' \
    "${ROOT}/THIRD_PARTY_CORE.md" 2>/dev/null ||
    fail "frozen_core" "packaged provenance hash unavailable"
  record "frozen_core=packaged_provenance_verified"
fi

if [[ "${MODE}" == "source" ]]; then
  DIST="${ROOT}/dist/release"
  [[ -d "${DIST}" ]] || fail "release_tree" "missing ${DIST} — run build-release + package-release first"
  bash "${ROOT}/scripts/verify-release.sh" >"${WORK}/verify-release.txt" 2>&1 ||
    fail "verify_release" "verify-release.sh failed"
  grep -E '1\.1\.0' "${DIST}/release-manifest-linux-amd64.json" >/dev/null ||
    fail "manifest_version" "amd64 manifest not 1.1.0"
  record "source_gate=release_artifacts_verified"
  finalize
fi

# --- installed node modes (local|live) ---
if [[ -z "${CTL}" ]]; then
  CTL="$(command -v nyxveilctl || true)"
fi
if [[ -z "${SERVER}" ]]; then
  for cand in /usr/local/sbin/nyxveil-server /usr/local/bin/nyxveil-server; do
    [[ -x "${cand}" ]] && SERVER="${cand}" && break
  done
fi
[[ -n "${CTL}" && -x "${CTL}" ]] || fail "ctl_binary" "nyxveilctl not executable"
[[ -n "${SERVER}" && -x "${SERVER}" ]] || fail "server_binary" "nyxveil-server not executable"

"${CTL}" version 2>&1 | sanitize >"${WORK}/versions.txt" ||
  fail "ctl_version" "nyxveilctl version failed"
"${SERVER}" version 2>&1 | sanitize >>"${WORK}/versions.txt" ||
  fail "server_version" "nyxveil-server version failed"
grep -Eq 'cli_version=1\.1\.0|nyxveilctl 1\.1\.0' "${WORK}/versions.txt" ||
  fail "cli_version" "expected 1.1.0"
grep -Eq 'nyxveil-server 1\.1\.0|installed_server_version=1\.1\.0|running_server_version=1\.1\.0' "${WORK}/versions.txt" ||
  fail "server_version" "expected 1.1.0"

[[ -s "${CONFIG}" ]] || fail "config" "${CONFIG} missing or empty"
[[ -s "${STATE_DIR}/node.key" ]] || fail "identity" "node.key missing or empty"
record "identity_files=present (contents not collected)"
sha256sum "${CONFIG}" "${SERVER}" "${CTL}" >"${WORK}/file-hashes.txt" 2>&1 ||
  fail "hashes" "could not hash non-secret files"

"${CTL}" status >"${WORK}/status.json" 2>"${WORK}/status.err" ||
  fail "status" "control status unavailable"
"${CTL}" health >"${WORK}/health.json" 2>"${WORK}/health.err" || {
  [[ "${MODE}" == "local" ]] || fail "health" "live mode requires healthy node"
  record "health=degraded (allowed in local mode)"
}

json_bool() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
raise SystemExit(0 if data.get(sys.argv[2]) is True else 1)
PY
}

python3 -m json.tool "${WORK}/status.json" >/dev/null ||
  fail "status_json" "invalid status JSON"
json_bool "${WORK}/status.json" running || fail "running" "server reports not running"
json_bool "${WORK}/status.json" tun_ready || fail "tun_ready" "TUN is not ready"
json_bool "${WORK}/status.json" bridge_ok || fail "bridge_ok" "bridge is not ready"
json_bool "${WORK}/status.json" ticket_keys_loaded ||
  fail "ticket_keys" "ticket verification keys are not loaded"
if ! json_bool "${WORK}/status.json" tls_ok &&
   ! json_bool "${WORK}/status.json" quic_ok; then
  fail "transports" "neither TLS nor QUIC is ready"
fi

CERT="${NYXVEIL_TLS_CERT:-${STATE_DIR}/tls.crt}"
KEY="${NYXVEIL_TLS_KEY:-${STATE_DIR}/tls.key}"
[[ -s "${CERT}" && -s "${KEY}" ]] || fail "tls_files" "TLS cert/key missing"
openssl x509 -in "${CERT}" -noout -subject -issuer -dates -ext subjectAltName \
  >"${WORK}/tls-certificate.txt" 2>&1 || fail "tls_certificate" "certificate parse failed"
openssl x509 -in "${CERT}" -pubkey -noout |
  openssl pkey -pubin -outform DER 2>/dev/null |
  sha256sum >"${WORK}/tls-spki-sha256.txt" ||
  fail "tls_spki" "could not derive certificate SPKI"
record "tls_private_key=presence_only (never collected)"

CP_URL="$(python3 - "${CONFIG}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("control_plane_url", ""))
PY
)"
if [[ -n "${CP_URL}" ]] && curl --silent --show-error --fail \
  --connect-timeout 5 --max-time 10 "${CP_URL%/}/health" \
  2>"${WORK}/cp-probe.err" | sanitize >"${WORK}/cp-health.txt"; then
  record "control_plane_probe=reachable"
else
  record "control_plane_probe=unreachable"
  [[ "${MODE}" != "live" ]] ||
    fail "control_plane_reachable" "live mode Control Plane probe failed"
fi
if [[ "${MODE}" == "live" ]]; then
  json_bool "${WORK}/status.json" cp_connected ||
    fail "cp_connected" "live mode requires cp_connected=true"
  ask_license_once
  # Catalog crypto gate uses license token only in-memory for curl Authorization.
  keys="$(curl --silent --show-error --fail --connect-timeout 5 --max-time 15 \
    -H "Authorization: Bearer ${LICENSE_TOKEN}" \
    "${CP_URL%/}/api/v1/catalog-keys" 2>"${WORK}/catalog-keys.err")" ||
    fail "catalog_keys" "catalog-keys request failed"
  printf '%s\n' "${keys}" | sanitize >"${WORK}/catalog-keys.json"
  catalog="$(curl --silent --show-error --fail --connect-timeout 5 --max-time 30 \
    -H "Authorization: Bearer ${LICENSE_TOKEN}" \
    "${CP_URL%/}/api/v1/catalog" 2>"${WORK}/catalog.err")" ||
    fail "catalog" "catalog request failed"
  printf '%s\n' "${catalog}" | sanitize >"${WORK}/catalog.sanitized.json"
  LICENSE_TOKEN=""
  unset GATE_LICENSE_TOKEN || true
  record "catalog_fetched=true (signature verification delegated to client/core tooling when available)"
fi

STOP_US="$(systemctl show nyxveil-server -p TimeoutStopUSec --value 2>/dev/null || true)"
[[ -n "${STOP_US}" && "${STOP_US}" != "infinity" ]] ||
  fail "shutdown_bound" "systemd TimeoutStopUSec is missing or unbounded"
record "graceful_stop=dry_check timeout_stop_usec=${STOP_US}"

if [[ "${GATE_STOP_TEST:-0}" == "1" ]]; then
  start_ns="$(date +%s%N)"
  systemctl stop nyxveil-server || fail "graceful_stop" "systemctl stop failed"
  elapsed_ms="$(( ($(date +%s%N) - start_ns) / 1000000 ))"
  record "graceful_stop=executed elapsed_ms=${elapsed_ms}"
  [[ "${elapsed_ms}" -lt 5000 ]] || fail "graceful_stop_slow" "stop took ${elapsed_ms}ms (>=5000)"
  systemctl start nyxveil-server || fail "restart_after_stop" "systemctl start failed"
fi

finalize
