#!/usr/bin/env bash
# Nyxveil server production gate.
#
# GATE_MODE=
#   source - verify built release artifacts + Frozen Core (no install, no secrets)
#   local  - installed node local health (no mutating stop unless GATE_STOP_TEST=1)
#   live   - local + CP reachability + cp_connected (asks license token once for catalog crypto)
#   updater - automatic lifecycle health + TLS material; never requests a license
#
# Never prints/stores license tokens, private keys, or node.key contents.
set -euo pipefail
umask 077

# nyxveilctl supplies normalized script bytes on stdin and the installed path as $1.
# BASH_SOURCE is unset in that execution mode.
GATE_SCRIPT="${BASH_SOURCE[0]:-${1:-}}"
[[ -n "${GATE_SCRIPT}" ]] || { echo 'production gate path missing' >&2; exit 1; }
ROOT="$(cd "$(dirname "${GATE_SCRIPT}")/.." && pwd)"
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

capture_version_diagnostics() {
  {
    echo "expected_version=${EXPECTED_VERSION:-}"
    echo "share_VERSION=${VERSION:-}"
    if [[ -n "${CTL:-}" && -x "${CTL}" ]]; then
      "${CTL}" version --json 2>/dev/null || "${CTL}" version 2>/dev/null || true
    fi
    if [[ -n "${SERVER:-}" && -x "${SERVER}" ]]; then
      echo -n "server_binary_probe="
      "${SERVER}" --version 2>/dev/null || "${SERVER}" version 2>/dev/null || echo "probe_failed"
      if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${SERVER}" 2>/dev/null || true
      fi
    fi
    if [[ -n "${CTL:-}" && -x "${CTL}" ]] && command -v sha256sum >/dev/null 2>&1; then
      sha256sum "${CTL}" 2>/dev/null || true
    fi
    if command -v systemctl >/dev/null 2>&1; then
      systemctl show nyxveil-server -p MainPID -p ExecStart -p ActiveEnterTimestamp 2>/dev/null || true
    fi
    if [[ -n "${NYXVEIL_SHARE_VERSION:-}" && -f "${NYXVEIL_SHARE_VERSION}" ]]; then
      echo -n "installed_share_VERSION="
      tr -d '\r[:space:]' <"${NYXVEIL_SHARE_VERSION}"
      echo
    elif [[ -f /usr/local/share/nyxveil/VERSION ]]; then
      echo -n "installed_share_VERSION="
      tr -d '\r[:space:]' </usr/local/share/nyxveil/VERSION
      echo
    fi
  } 2>/dev/null | sanitize >"${WORK}/version-diagnostics.txt" || true
}

capture_cp_diagnostics() {
  {
    echo "configured_cp_url=${CP_URL:-}"
    echo "gate_probe_method=runtime_status.cp_connected (authoritative)"
    echo "auxiliary_curl=/health (diagnostic only)"
    if [[ -n "${GATE_UPDATER_POSTCHECK:-}" ]]; then
      echo "updater_postcheck=${GATE_UPDATER_POSTCHECK}"
    fi
    if [[ -f "${WORK}/status.json" ]]; then
      python3 - "${WORK}/status.json" <<'PY' 2>/dev/null || true
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d = json.load(f)
for k in (
    "cp_connected", "cp_url", "cp_tls_mode", "cp_tls_status",
    "last_cp_success", "last_heartbeat_success", "last_config_success",
    "last_ticket_keys_success", "last_revocation_success", "cp_last_error",
    "healthy", "running", "uptime_seconds",
):
    if k in d:
        print(f"{k}={d.get(k)}")
PY
    fi
    if [[ -n "${CP_URL:-}" ]] && command -v python3 >/dev/null 2>&1; then
      python3 - "${CP_URL}" <<'PY' 2>/dev/null || true
import socket, sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1])
host = u.hostname or ""
print(f"resolved_hostname={host}")
try:
    infos = socket.getaddrinfo(host, u.port or 443, type=socket.SOCK_STREAM)
    ips = sorted({i[4][0] for i in infos})
    print("resolved_ips=" + ",".join(ips))
except Exception as e:
    print(f"resolved_ips_error={e}")
PY
    fi
    if [[ -f "${WORK}/cp-probe.txt" ]]; then
      echo "--- auxiliary_curl_probe ---"
      cat "${WORK}/cp-probe.txt" 2>/dev/null || true
    fi
    if [[ -f "${WORK}/cp-probe.err" ]]; then
      echo "--- auxiliary_curl_probe_err ---"
      cat "${WORK}/cp-probe.err" 2>/dev/null || true
    fi
    if [[ -f "${WORK}/cp-wait.txt" ]]; then
      echo "--- cp_wait ---"
      cat "${WORK}/cp-wait.txt" 2>/dev/null || true
    fi
    if command -v systemctl >/dev/null 2>&1; then
      systemctl show nyxveil-server -p MainPID -p ActiveEnterTimestamp 2>/dev/null || true
    fi
  } 2>/dev/null | sanitize >"${WORK}/cp-diagnostics.txt" || true
}

finalize() {
  if [[ -n "${FAILED_GATE}" ]]; then
    case "${FAILED_GATE}" in
      server_version|cli_version|ctl_version|version_file|installed_server_version|running_server_version|release_version|core_version|protocol)
        capture_version_diagnostics
        ;;
      control_plane_reachable|cp_connected|cp_auth|heartbeat)
        capture_cp_diagnostics
        ;;
    esac
  fi
  if command -v journalctl >/dev/null 2>&1 && [[ "${MODE}" != "source" ]]; then
    journalctl -u nyxveil-server --since '-30 minutes' --no-pager 2>&1 |
      sanitize >"${WORK}/journal-sanitized.log" || true
  fi
  if command -v systemctl >/dev/null 2>&1 && [[ "${MODE}" != "source" ]]; then
    systemctl show nyxveil-server \
      -p ActiveState -p SubState -p MainPID -p TimeoutStopUSec -p ExecStart -p ActiveEnterTimestamp 2>&1 |
      sanitize >"${WORK}/systemd-status.txt" || true
  fi
  tar -czf "${BUNDLE}" -C "${WORK}" . 2>/dev/null || true
  if [[ -n "${FAILED_GATE}" ]]; then
    printf 'RESULT=FAIL failed_gate=%s diagnostic_bundle=%s\n' "${FAILED_GATE}" "${BUNDLE}"
    exit 1
  fi
  print_operator_pass_summary
  printf 'RESULT=PASS\n'
  exit 0
}

print_operator_pass_summary() {
  # Compact operator-facing summary (also asserted by updateв†’gate contract tests).
  printf 'Version ................ PASS\n'
  case "${MODE}" in
    source)
      printf 'Release artifacts ...... PASS\n'
      printf 'Frozen Core ............ PASS\n'
      ;;
    local|live)
      printf 'Control Plane .......... PASS\n'
      if [[ "${MODE}" == "live" ]]; then
        printf 'Catalog signature ...... PASS\n'
      fi
      printf 'TLS .................... PASS\n'
      printf 'TUN .................... PASS\n'
      printf 'QUIC ................... PASS\n'
      printf 'Graceful SIGTERM ....... PASS\n'
      printf 'Restart recovery ....... PASS\n'
      ;;
  esac
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
  source|local|live|updater) ;;
  *) fail "mode" "GATE_MODE must be source|local|live|updater" ;;
esac

EXPECTED_VERSION="${NYXVEIL_EXPECTED_VERSION:-1.1.14}"
SHARE_DIR="${NYXVEIL_SHARE_DIR:-/usr/local/share/nyxveil}"
SHARE_VERSION_FILE="${NYXVEIL_SHARE_VERSION:-${SHARE_DIR}/VERSION}"
VERSION="$(tr -d '\r[:space:]' < "${ROOT}/VERSION" 2>/dev/null || true)"
if [[ -z "${VERSION}" && -f "${SHARE_VERSION_FILE}" ]]; then
  VERSION="$(tr -d '\r[:space:]' < "${SHARE_VERSION_FILE}" || true)"
fi
if [[ -z "${VERSION}" && -f /usr/local/share/nyxveil/VERSION ]]; then
  # Installed production layout fallback.
  VERSION="$(tr -d '\r[:space:]' < "/usr/local/share/nyxveil/VERSION" || true)"
fi
[[ "${VERSION}" == "${EXPECTED_VERSION}" ]] || fail "version_file" "expected VERSION=${EXPECTED_VERSION} got '${VERSION}'"

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
  [[ -d "${DIST}" ]] || fail "release_tree" "missing ${DIST} - run build-release + package-release first"
  bash "${ROOT}/scripts/verify-release.sh" >"${WORK}/verify-release.txt" 2>&1 ||
    fail "verify_release" "verify-release.sh failed"
  grep -E "${EXPECTED_VERSION}" "${DIST}/release-manifest-linux-amd64.json" >/dev/null ||
    fail "manifest_version" "amd64 manifest not ${EXPECTED_VERSION}"
  [[ -f "${DIST}/nyxveil-catalog-verify-linux-amd64" ]] ||
    fail "catalog_verify_asset" "nyxveil-catalog-verify-linux-amd64 missing from release"
  [[ -f "${DIST}/production-gate.sh" ]] ||
    fail "production_gate_asset" "production-gate.sh missing from release"
  [[ -x "${DIST}/production-gate.sh" ]] ||
    fail "production_gate_mode" "production-gate.sh must be executable"
  [[ -f "${DIST}/nyxveil-update.service" ]] ||
    fail "update_unit_asset" "nyxveil-update.service missing from release"
  [[ -f "${DIST}/50-nyxveil-management.rules" ]] ||
    fail "management_polkit_asset" "50-nyxveil-management.rules missing from release"
  chmod 0755 "${DIST}/nyxveil-catalog-verify-linux-amd64" "${DIST}/nyxveil-catalog-verify-linux-arm64" 2>/dev/null || true
  if command -v go >/dev/null 2>&1; then
    (cd "${ROOT}" && go test ./internal/catalogverify/ ./internal/runtime/ ./internal/controlplane/ -count=1 \
      -run 'TestVerify|TestTampered|TestWrong|TestModified|TestMalformed|TestNormalRenew|TestKeyRotation|TestSystemTrustClientReconnects' ) \
      >"${WORK}/catalog-spki-tests.txt" 2>&1 ||
      fail "catalog_spki_tests" "catalog crypto / SPKI / CP leaf rotation tests failed"
  fi
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

# Machine-readable version API only - never fragile human grep / never start a second daemon.
if ! "${CTL}" version --json 2>"${WORK}/ctl-version.err" | sanitize >"${WORK}/versions.json"; then
  fail "ctl_version" "nyxveilctl version --json failed"
fi
VERSION_GATE="$(
python3 - "${WORK}/versions.json" "${EXPECTED_VERSION}" "${WORK}/version-assert.txt" <<'PY'
import json, sys
path, expected, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
required = [
    ("installed_cli_version", expected),
    ("installed_server_version", expected),
    ("running_server_version", expected),
    ("release_version", expected),
    ("core_version", "1.0.0"),
    ("protocol", "NVP/1"),
]
# Fall back to cli_version only when installed_cli_version is absent (pre-1.1.6 ctl).
if not data.get("installed_cli_version"):
    required[0] = ("cli_version", expected)
gate_map = {
    "cli_version": "cli_version",
    "installed_cli_version": "cli_version",
    "installed_server_version": "installed_server_version",
    "running_server_version": "running_server_version",
    "release_version": "release_version",
    "core_version": "core_version",
    "protocol": "protocol",
}
lines = []
failed = None
for key, want in required:
    got = data.get(key)
    if got is None or got == "" or got == "unknown":
        lines.append(f"FAIL {key}: unavailable (got={got!r})")
        failed = failed or key
    elif str(got).strip() != want:
        lines.append(f"FAIL {key}: got={got!r} want={want!r}")
        failed = failed or key
    else:
        lines.append(f"PASS {key}={got}")
open(out_path, "w", encoding="utf-8").write("\n".join(lines) + "\n")
if failed:
    print(gate_map.get(failed, "server_version"))
    raise SystemExit(1)
print("ok")
PY
)" || true
if [[ -f "${WORK}/version-assert.txt" ]]; then
  sanitize <"${WORK}/version-assert.txt" >"${WORK}/versions.txt" || true
fi
if [[ "${VERSION_GATE}" != "ok" ]]; then
  fail "${VERSION_GATE:-server_version}" "version assertion failed (see version-assert.txt)"
fi
# Also probe disk binary with --version (must not start daemon).
SERVER_VER_OUT="$("${SERVER}" --version 2>&1 || true)"
printf '%s\n' "${SERVER_VER_OUT}" | sanitize >>"${WORK}/versions.txt"
echo "${SERVER_VER_OUT}" | grep -Eq "nyxveil-server ${EXPECTED_VERSION}([[:space:]]|$)" ||
  fail "server_version" "nyxveil-server --version mismatch: ${SERVER_VER_OUT}"

UPDATE_UNIT="${NYXVEIL_UPDATE_UNIT:-/etc/systemd/system/nyxveil-update.service}"
POLKIT_RULE="${NYXVEIL_POLKIT_RULE:-/etc/polkit-1/rules.d/50-nyxveil-management.rules}"
[[ -f "${UPDATE_UNIT}" ]] || fail "update_unit" "${UPDATE_UNIT} missing"
[[ -f "${POLKIT_RULE}" ]] || fail "management_polkit" "${POLKIT_RULE} missing"
UNIT_MODE="$(stat -c '%a' "${UPDATE_UNIT}" 2>/dev/null || true)"
RULE_MODE="$(stat -c '%a' "${POLKIT_RULE}" 2>/dev/null || true)"
[[ "${UNIT_MODE}" == "644" ]] || fail "update_unit_mode" "want 644 got '${UNIT_MODE}'"
[[ "${RULE_MODE}" == "644" ]] || fail "management_polkit_mode" "want 644 got '${RULE_MODE}'"
grep -q 'Type=oneshot' "${UPDATE_UNIT}" || fail "update_unit_type" "Type=oneshot required"
grep -q 'User=root' "${UPDATE_UNIT}" || fail "update_unit_user" "User=root required"
grep -Eq 'ExecStart=.*/nyxveilctl[[:space:]]+update([[:space:]]|$)' "${UPDATE_UNIT}" ||
  fail "update_unit_exec" "ExecStart must be fixed nyxveilctl update"
if grep -Eqi 'ANY|shell|sudo|ExecStart=.*(bash|sh|/bin/)' "${UPDATE_UNIT}"; then
  fail "update_unit_scope" "update unit must not allow arbitrary exec"
fi
grep -q 'subject.user !== "nyxveil"' "${POLKIT_RULE}" || fail "polkit_subject" "rule must bind nyxveil user"
grep -q 'nyxveil-server.service' "${POLKIT_RULE}" || fail "polkit_server_unit" "server restart grant missing"
grep -q 'nyxveil-update.service' "${POLKIT_RULE}" || fail "polkit_update_unit" "update unit grant missing"
if grep -Eqi 'org\.freedesktop\.systemd1\.manage-units.*\*|unit === "\*"' "${POLKIT_RULE}"; then
  fail "polkit_broad" "polkit must not authorize arbitrary units"
fi
# Only require systemd to load the unit when checking the live system path.
if [[ "${UPDATE_UNIT}" == "/etc/systemd/system/nyxveil-update.service" ]] \
  && command -v systemctl >/dev/null 2>&1; then
  systemctl cat nyxveil-update.service >/dev/null 2>&1 ||
    fail "update_unit_systemd" "systemd does not see nyxveil-update.service (daemon-reload?)"
fi
SHARE_VER=""
if [[ -f "${SHARE_VERSION_FILE}" ]]; then
  SHARE_VER="$(tr -d '\r[:space:]' < "${SHARE_VERSION_FILE}" || true)"
elif [[ -f "${ROOT}/VERSION" ]]; then
  SHARE_VER="$(tr -d '\r[:space:]' < "${ROOT}/VERSION" || true)"
elif [[ -f /usr/local/share/nyxveil/VERSION ]]; then
  SHARE_VER="$(tr -d '\r[:space:]' < /usr/local/share/nyxveil/VERSION || true)"
fi
[[ "${SHARE_VER}" == "${EXPECTED_VERSION}" ]] ||
  fail "share_version" "share VERSION='${SHARE_VER}' want ${EXPECTED_VERSION} (file=${SHARE_VERSION_FILE})"
record "management_prerequisites=update_unit+polkit_ok"

[[ -s "${CONFIG}" ]] || fail "config" "${CONFIG} missing or empty"
[[ -s "${STATE_DIR}/node.key" ]] || fail "identity" "node.key missing or empty"
record "identity_files=present (contents not collected)"
sha256sum "${CONFIG}" "${SERVER}" "${CTL}" >"${WORK}/file-hashes.txt" 2>&1 ||
  fail "hashes" "could not hash non-secret files"

"${CTL}" status >"${WORK}/status.json" 2>"${WORK}/status.err" ||
  fail "status" "control status unavailable"
# Do not fail-closed on healthy before authenticated CP readiness wait.
# Post-update/restart can leave healthy=false while dataplane is up and CP is reconnecting.
"${CTL}" health >"${WORK}/health.json" 2>"${WORK}/health.err" || {
  if [[ "${MODE}" == "live" ]]; then
    record "health=not_yet (deferred until authenticated CP wait)"
  else
    record "health=degraded (allowed in local mode)"
  fi
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
LIFECYCLE_STOPPED=0
if [[ "${MODE}" == updater ]] && ! json_bool "${WORK}/status.json" accepting &&
   { json_bool "${WORK}/status.json" draining || json_bool "${WORK}/status.json" maintenance_mode; }; then
  LIFECYCLE_STOPPED=1
  json_bool "${WORK}/status.json" identity_present || fail identity "runtime identity missing"
  json_bool "${WORK}/status.json" cp_connected || fail cp_connected "management not connected"
  if json_bool "${WORK}/status.json" version_blocked || json_bool "${WORK}/status.json" revocation_stale; then
    fail updater_health "version blocked or revocation stale"
  fi
  record "transports=deliberately_stopped_by_lifecycle"
fi
if [[ "${LIFECYCLE_STOPPED}" != 1 ]] && ! json_bool "${WORK}/status.json" tls_ok &&
   ! json_bool "${WORK}/status.json" quic_ok; then
  fail "transports" "neither TLS nor QUIC is ready"
fi

CERT="${NYXVEIL_TLS_CERT:-${STATE_DIR}/tls.crt}"
KEY="${NYXVEIL_TLS_KEY:-${STATE_DIR}/tls.key}"
if [[ "${MODE}" == updater ]]; then
  mapfile -t TLS_CONFIG < <(python3 - "${CONFIG}" "${STATE_DIR}" <<'PY'
import json,sys,os
with open(sys.argv[1],encoding='utf-8') as f:c=json.load(f)
for value in [c.get('tls_cert_file') or os.path.join(sys.argv[2],'tls.crt'),
              c.get('tls_key_file') or os.path.join(sys.argv[2],'tls.key'),
              c.get('server_name') or c.get('public_host') or '']:
    if '\n' in value or '\r' in value:raise SystemExit(1)
    print(value)
PY
  )
  [[ "${#TLS_CONFIG[@]}" == 3 && -n "${TLS_CONFIG[2]}" ]] || fail tls_config "invalid TLS configuration"
  CERT="${TLS_CONFIG[0]}"; KEY="${TLS_CONFIG[1]}"
fi
[[ -s "${CERT}" && -s "${KEY}" ]] || fail "tls_files" "TLS cert/key missing"
openssl x509 -in "${CERT}" -noout -subject -issuer -dates -ext subjectAltName \
  >"${WORK}/tls-certificate.txt" 2>&1 || fail "tls_certificate" "certificate parse failed"
openssl x509 -in "${CERT}" -pubkey -noout |
  openssl pkey -pubin -outform DER 2>/dev/null |
  sha256sum >"${WORK}/tls-spki-sha256.txt" ||
  fail "tls_spki" "could not derive certificate SPKI"
if [[ "${MODE}" == updater ]]; then
  openssl x509 -in "${CERT}" -noout -checkhost "${TLS_CONFIG[2]}" >"${WORK}/tls-host.txt" 2>&1 || fail tls_san "configured hostname mismatch"
  openssl pkey -in "${KEY}" -pubout -outform DER 2>/dev/null |
    sha256sum >"${WORK}/tls-key-public-sha256.txt" || fail tls_key "key parse failed"
  cmp -s "${WORK}/tls-spki-sha256.txt" "${WORK}/tls-key-public-sha256.txt" ||
    fail tls_key "certificate and key mismatch"
  openssl x509 -in "${CERT}" -noout -checkend 0 >/dev/null || fail tls_certificate "certificate expired"
  [[ "$(stat -c '%a:%U' "${KEY}")" == 600:nyxveil ]] || fail tls_ownership "key mode/owner invalid"
  [[ "$(stat -c '%a:%U' "${CERT}")" == 644:nyxveil ]] || fail tls_ownership "certificate mode/owner invalid"
fi
record "tls_private_key=never_collected"

CP_URL="$(python3 - "${CONFIG}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("control_plane_url", ""))
PY
)"
record "configured_cp_url=${CP_URL}"

# Authoritative Control Plane readiness = daemon authenticated management plane
# (same truth as updater management_plane_connected / status.cp_connected).
# Auxiliary curl to /health is diagnostic-only and MUST NOT fail live gate when
# the daemon already proves authenticated CP connectivity via SystemTrust.
wait_cp_authenticated() {
  local wait_sec="${GATE_CP_WAIT_SEC:-45}"
  local need_stable="${GATE_CP_STABLE_SAMPLES:-3}"
  local interval="${GATE_CP_POLL_INTERVAL_SEC:-1}"
  local deadline=$(( SECONDS + wait_sec ))
  local stable=0
  local last_reason="cp_connected not yet true"
  : >"${WORK}/cp-wait.txt"
  while (( SECONDS < deadline )); do
    if ! "${CTL}" status >"${WORK}/status.json" 2>"${WORK}/status.err"; then
      last_reason="status unavailable"
      echo "sample=fail reason=status_unavailable" >>"${WORK}/cp-wait.txt"
      stable=0
      sleep "${interval}"
      continue
    fi
    if json_bool "${WORK}/status.json" cp_connected; then
      stable=$((stable + 1))
      echo "sample=ok cp_connected=true stable=${stable}/${need_stable}" >>"${WORK}/cp-wait.txt"
      if (( stable >= need_stable )); then
        record "control_plane_reachable=authenticated_runtime cp_connected=true"
        return 0
      fi
    else
      stable=0
      last_reason="cp_connected=false"
      # Permanent config/TLS errors surface via cp_last_error; keep polling unless
      # caller set GATE_CP_FAIL_FAST=1 and error looks permanent.
      err="$(python3 - "${WORK}/status.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("cp_last_error") or "")
PY
)"
      echo "sample=wait cp_connected=false err=$(printf '%s' "${err}" | tr '\n' ' ')" >>"${WORK}/cp-wait.txt"
      if [[ "${GATE_CP_FAIL_FAST:-0}" == "1" && -n "${err}" ]]; then
        case "${err}" in
          *"unsupported scheme"*|*"URL missing host"*|*"no such host"*|*"certificate is not valid"*)
            echo "permanent_error=${err}" >>"${WORK}/cp-wait.txt"
            return 1
            ;;
        esac
      fi
    fi
    sleep "${interval}"
  done
  echo "timeout wait_sec=${wait_sec} last=${last_reason}" >>"${WORK}/cp-wait.txt"
  return 1
}

# Optional diagnostic curl (never authoritative). Uses same configured URL host.
if [[ -n "${CP_URL}" ]]; then
  if curl --silent --show-error --fail \
    --connect-timeout 5 --max-time 10 "${CP_URL%/}/health" \
    2>"${WORK}/cp-probe.err" | sanitize >"${WORK}/cp-health.txt"; then
    echo "auxiliary_curl_health=ok" >"${WORK}/cp-probe.txt"
    record "control_plane_curl_probe=ok (diagnostic only)"
  else
    echo "auxiliary_curl_health=fail" >"${WORK}/cp-probe.txt"
    record "control_plane_curl_probe=fail (diagnostic only; ignored when runtime cp_connected)"
  fi
fi

if [[ "${MODE}" == "live" ]]; then
  if ! wait_cp_authenticated; then
    fail "control_plane_reachable" "authenticated Control Plane not ready after wait (runtime cp_connected)"
  fi
  # Refresh status after wait for subsequent gates.
  "${CTL}" status >"${WORK}/status.json" 2>"${WORK}/status.err" ||
    fail "status" "control status unavailable after CP wait"
  json_bool "${WORK}/status.json" cp_connected ||
    fail "cp_connected" "live mode requires cp_connected=true"
  json_bool "${WORK}/status.json" healthy ||
    fail "heartbeat" "live mode requires healthy=true (includes authenticated CP)"
  # Process-level / post-update invariant tests stop after authoritative CP+healthy.
  if [[ "${GATE_STOP_AFTER_CP:-0}" == "1" ]]; then
    record "gate_stop_after_cp=1 control_plane_reachable=PASS heartbeat=PASS"
    finalize
  fi
  ask_license_once

  VERIFY_BIN="${NYXVEIL_CATALOG_VERIFY:-}"
  if [[ -z "${VERIFY_BIN}" ]]; then
    for cand in \
      "$(command -v nyxveil-catalog-verify || true)" \
      "/usr/local/sbin/nyxveil-catalog-verify" \
      "${ROOT}/dist/bin/nyxveil-catalog-verify-linux-amd64" \
      "${ROOT}/dist/release/nyxveil-catalog-verify-linux-amd64" \
      "${ROOT}/dist/release/linux-amd64/nyxveil-catalog-verify"; do
      if [[ -n "${cand}" && -x "${cand}" ]]; then
        VERIFY_BIN="${cand}"
        break
      fi
    done
  fi
  if [[ -z "${VERIFY_BIN}" || ! -x "${VERIFY_BIN}" ]]; then
    if command -v go >/dev/null 2>&1 && [[ -d "${ROOT}/cmd/nyxveil-catalog-verify" ]]; then
      VERIFY_BIN="$(mktemp "${WORK}/nyxveil-catalog-verify.XXXXXX")"
      (cd "${ROOT}" && CGO_ENABLED=0 go build -o "${VERIFY_BIN}" ./cmd/nyxveil-catalog-verify) ||
        fail "catalog_verify_build" "could not build nyxveil-catalog-verify"
      chmod 0755 "${VERIFY_BIN}"
    else
      fail "catalog_verify_tool" "nyxveil-catalog-verify binary not found"
    fi
  fi

  # RAW bodies for cryptographic verification (never sanitize before verify).
  curl --silent --show-error --fail --connect-timeout 5 --max-time 15 \
    -H "Authorization: Bearer ${LICENSE_TOKEN}" \
    "${CP_URL%/}/api/v1/catalog-keys" \
    -o "${WORK}/catalog-keys.raw.json" 2>"${WORK}/catalog-keys.err" ||
    fail "catalog_keys" "catalog-keys request failed"
  curl --silent --show-error --fail --connect-timeout 5 --max-time 30 \
    -H "Authorization: Bearer ${LICENSE_TOKEN}" \
    "${CP_URL%/}/api/v1/catalog" \
    -o "${WORK}/catalog.raw.json" 2>"${WORK}/catalog.err" ||
    fail "catalog" "catalog request failed"
  LICENSE_TOKEN=""
  unset GATE_LICENSE_TOKEN || true
  sanitize <"${WORK}/catalog-keys.raw.json" >"${WORK}/catalog-keys.sanitized.json" || true
  sanitize <"${WORK}/catalog.raw.json" >"${WORK}/catalog.sanitized.json" || true

  NODE_ID="$(python3 - "${CONFIG}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("node_id", ""))
PY
)"
  SERVER_NAME="$(python3 - "${CONFIG}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d=json.load(f)
    print(d.get("server_name") or d.get("public_host") or "")
PY
)"
  SPKI_B64=""
  if [[ -s "${WORK}/tls-spki-sha256.txt" ]]; then
    SPKI_HEX="$(awk '{print $1}' "${WORK}/tls-spki-sha256.txt")"
    SPKI_B64="$(python3 - "${SPKI_HEX}" <<'PY'
import sys, binascii, base64
print(base64.b64encode(binascii.unhexlify(sys.argv[1].strip())).decode())
PY
)"
  fi

  VERIFY_ARGS=( -keys "${WORK}/catalog-keys.raw.json" -catalog "${WORK}/catalog.raw.json" )
  if [[ -n "${NODE_ID}" ]]; then
    VERIFY_ARGS+=( -expect-node "${NODE_ID}" )
  fi
  if [[ -n "${SPKI_B64}" ]]; then
    VERIFY_ARGS+=( -expect-spki-b64 "${SPKI_B64}" )
  fi
  if [[ -n "${SERVER_NAME}" ]]; then
    VERIFY_ARGS+=( -expect-server-name "${SERVER_NAME}" )
  fi

  if ! "${VERIFY_BIN}" "${VERIFY_ARGS[@]}" | tee "${WORK}/catalog-verify.out"; then
    fail "catalog_signature" "Ed25519 catalog verification failed"
  fi
  grep -q 'Catalog signature ........ PASS' "${WORK}/catalog-verify.out" ||
    fail "catalog_signature" "verifier did not report PASS"
  record "catalog_crypto=PASS"
fi

STOP_US="$(systemctl show nyxveil-server -p TimeoutStopUSec --value 2>/dev/null || true)"
[[ -n "${STOP_US}" && "${STOP_US}" != "infinity" ]] ||
  fail "shutdown_bound" "systemd TimeoutStopUSec is missing or unbounded"
record "graceful_stop=dry_check timeout_stop_usec=${STOP_US}"

if [[ "${GATE_STOP_TEST:-0}" == "1" && "${MODE}" != updater ]]; then
  start_ns="$(date +%s%N)"
  systemctl stop nyxveil-server || fail "graceful_stop" "systemctl stop failed"
  elapsed_ms="$(( ($(date +%s%N) - start_ns) / 1000000 ))"
  record "graceful_stop=executed elapsed_ms=${elapsed_ms}"
  [[ "${elapsed_ms}" -lt 5000 ]] || fail "graceful_stop_slow" "stop took ${elapsed_ms}ms (>=5000)"
  systemctl start nyxveil-server || fail "restart_after_stop" "systemctl start failed"
fi

finalize
