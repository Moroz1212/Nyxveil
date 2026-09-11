#!/usr/bin/env bash
# Process-level live-final-update integration tests (fixture release + stub ctl).
set -euo pipefail
export NYXVEIL_TEST_MODE=1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CASE="${1:?case name required}"
die() { echo "test-live-final-cases[$CASE]: $*" >&2; exit 1; }

command -v python3 >/dev/null 2>&1 || die "python3 required"
command -v curl >/dev/null 2>&1 || die "curl required"

need_jq() { command -v jq >/dev/null 2>&1 || command -v python3 >/dev/null 2>&1 || die "jq or python3 required"; }
need_go() {
  if command -v go >/dev/null 2>&1; then
    return 0
  fi
  if [[ -x "/mnt/c/Program Files/Go/bin/go.exe" ]]; then
    export PATH="/mnt/c/Program Files/Go/bin:${PATH}"
    return 0
  fi
  die "go required"
}

host_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch $(uname -m)" ;;
  esac
}

write_stub_ctl() {
  local dest="$1"
  cat > "${dest}" <<'CTL'
#!/usr/bin/env bash
set -euo pipefail
export NYXVEIL_TEST_MODE=1
if [[ "${1:-}" != "update" ]]; then
  echo "stub-ctl $*"; exit 0
fi
MANIFEST="${2:?manifest}"
python3 - <<'PY'
import hashlib, json, os, pathlib, urllib.request
source = os.environ["MANIFEST"]
with urllib.request.urlopen(source, timeout=15) if source.startswith(("http://", "https://")) else open(source, "rb") as stream:
    m = json.load(stream)
bin_dir = pathlib.Path(os.environ.get("NYXVEIL_BIN_DIR", "/usr/local/sbin"))
share = pathlib.Path(os.environ.get("NYXVEIL_SHARE_DIR", "/usr/local/share/nyxveil"))
dest = {
  "nyxveil-server": bin_dir / "nyxveil-server",
  "nyxveilctl": bin_dir / "nyxveilctl",
  "nyxveil-catalog-verify": bin_dir / "nyxveil-catalog-verify",
  "production-gate": share / "scripts" / "production-gate.sh",
  "share-version": share / "VERSION",
  "share-third-party-core": share / "THIRD_PARTY_CORE.md",
}
for a in m["assets"]:
  name = a["name"]
  if name not in dest:
    continue
  data = urllib.request.urlopen(a["url"], timeout=15).read()
  got = hashlib.sha256(data).hexdigest()
  if got != a["sha256"]:
    raise SystemExit(f"hash mismatch {name}")
  p = dest[name]
  p.parent.mkdir(parents=True, exist_ok=True)
  p.write_bytes(data)
  os.chmod(p, int(a.get("mode") or "0755", 8))
print("stub-ctl: updated to", m["version"])
PY
CTL
  # Inject MANIFEST into stub environment via wrapper line.
  # The heredoc references os.environ["MANIFEST"]; export it at call site.
  chmod 0755 "${dest}"
}

make_fixture_manifests() {
  local out="$1"
  local version="$2"
  local base="$3"
  (
    cd "${ROOT}"
    go run ./scripts/make-release-manifest.go \
      -version "${version}" \
      -out "${out}" \
      -base-url "${base}" \
      -amd64-server "${out}/nyxveil-server-linux-amd64" \
      -amd64-ctl "${out}/nyxveilctl-linux-amd64" \
      -amd64-catalog "${out}/nyxveil-catalog-verify-linux-amd64" \
      -arm64-server "${out}/nyxveil-server-linux-arm64" \
      -arm64-ctl "${out}/nyxveilctl-linux-arm64" \
      -arm64-catalog "${out}/nyxveil-catalog-verify-linux-arm64" \
      -production-gate "${out}/production-gate.sh" \
      -share-version "${out}/VERSION" \
      -share-third-party "${out}/THIRD_PARTY_CORE.md" \
      -update-service "${out}/nyxveil-update.service" \
      -management-polkit "${out}/50-nyxveil-management.rules" >/dev/null
  )
}

write_sums() {
  local out="$1"
  local crlf="${2:-0}"
  (
    cd "${out}"
    sha256sum \
      nyxveil-server-linux-amd64 nyxveilctl-linux-amd64 nyxveil-catalog-verify-linux-amd64 \
      nyxveil-server-linux-arm64 nyxveilctl-linux-arm64 nyxveil-catalog-verify-linux-arm64 \
      production-gate.sh VERSION THIRD_PARTY_CORE.md \
      release-manifest-linux-amd64.json release-manifest-linux-arm64.json \
      bootstrap-cli-update.sh live-final-update.sh \
      | tr -d '\r' | sed 's/ \*/  /' > SHA256SUMS
  )
  if [[ "${crlf}" == "1" ]]; then
    python3 - "${out}/SHA256SUMS" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
text = p.read_text(encoding="utf-8").replace("\r\n", "\n").replace("\n", "\r\n")
p.write_bytes(text.encode("utf-8"))
PY
  fi
}

build_fixture_assets() {
  local out="$1"
  local version="${2:-1.1.3}"
  mkdir -p "${out}"
  for arch in amd64 arm64; do
    printf '%s\n' '#!/usr/bin/env bash' "echo stub-server-${arch}" > "${out}/nyxveil-server-linux-${arch}"
    write_stub_ctl "${out}/nyxveilctl-linux-${arch}"
    # Ensure MANIFEST env is visible inside python - export via wrapper:
    cat > "${out}/nyxveilctl-linux-${arch}" <<CTL
#!/usr/bin/env bash
set -euo pipefail
export NYXVEIL_TEST_MODE=1
if [[ "\${1:-}" != "update" ]]; then
  echo "stub-ctl \$*"; exit 0
fi
export MANIFEST="\${2:?manifest}"
python3 - <<'PY'
import hashlib, json, os, pathlib, urllib.request
source = os.environ["MANIFEST"]
with urllib.request.urlopen(source, timeout=15) if source.startswith(("http://", "https://")) else open(source, "rb") as stream:
    m = json.load(stream)
bin_dir = pathlib.Path(os.environ.get("NYXVEIL_BIN_DIR", "/usr/local/sbin"))
share = pathlib.Path(os.environ.get("NYXVEIL_SHARE_DIR", "/usr/local/share/nyxveil"))
dest = {
  "nyxveil-server": bin_dir / "nyxveil-server",
  "nyxveilctl": bin_dir / "nyxveilctl",
  "nyxveil-catalog-verify": bin_dir / "nyxveil-catalog-verify",
  "production-gate": share / "scripts" / "production-gate.sh",
  "share-version": share / "VERSION",
  "share-third-party-core": share / "THIRD_PARTY_CORE.md",
}
for a in m["assets"]:
  name = a["name"]
  if name not in dest:
    continue
  data = urllib.request.urlopen(a["url"], timeout=15).read()
  got = hashlib.sha256(data).hexdigest()
  if got != a["sha256"]:
    raise SystemExit(f"hash mismatch {name}")
  p = dest[name]
  p.parent.mkdir(parents=True, exist_ok=True)
  p.write_bytes(data)
  os.chmod(p, int(a.get("mode") or "0755", 8))
print("stub-ctl: updated to", m["version"])
PY
CTL
    printf '%s\n' '#!/usr/bin/env bash' "echo catalog-${arch}" > "${out}/nyxveil-catalog-verify-linux-${arch}"
    chmod 0755 "${out}/nyxveil-server-linux-${arch}" \
      "${out}/nyxveilctl-linux-${arch}" \
      "${out}/nyxveil-catalog-verify-linux-${arch}"
  done
  printf '%s\n' '#!/usr/bin/env bash' 'echo RESULT=PASS' > "${out}/production-gate.sh"
  printf '%s\n' "${version}" > "${out}/VERSION"
  printf '%s\n' 'frozen-core' > "${out}/THIRD_PARTY_CORE.md"
  printf '%s\n' '[Unit]' 'Description=Nyxveil update' > "${out}/nyxveil-update.service"
  printf '%s\n' '// polkit stub' > "${out}/50-nyxveil-management.rules"
  cp -a "${ROOT}/scripts/bootstrap-cli-update.sh" "${out}/bootstrap-cli-update.sh"
  cp -a "${ROOT}/scripts/live-final-update.sh" "${out}/live-final-update.sh"
  chmod 0755 "${out}/production-gate.sh" "${out}/bootstrap-cli-update.sh" "${out}/live-final-update.sh"
  host_arch > "${out}/.arch"
}

start_http() {
  local root="$1"
  local port_file="$2"
  # Do not inherit command-substitution stdout: the background server otherwise
  # keeps PID="$(start_http ...)" waiting for EOF forever.
  python3 - "${root}" "${port_file}" >"${root}/http.log" 2>&1 <<'PY' &
import http.server, socketserver, pathlib, sys, functools
port_file = pathlib.Path(sys.argv[2])
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass
with socketserver.TCPServer(("127.0.0.1", 0), functools.partial(H, directory=sys.argv[1])) as httpd:
    port_file.write_text(str(httpd.server_address[1]), encoding="ascii")
    httpd.timeout = 1
    import time
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        httpd.handle_request()
PY
  echo $!
}

wait_port() {
  local port_file="$1"
  local i
  for i in $(seq 1 100); do
    [[ -f "${port_file}" ]] && return 0
    sleep 0.05
  done
  die "HTTP server failed to start"
}

assert_install_complete() {
  local prefix="$1"
  test -x "${prefix}/usr/local/sbin/nyxveil-server"
  test -x "${prefix}/usr/local/sbin/nyxveilctl"
  test -x "${prefix}/usr/local/sbin/nyxveil-catalog-verify"
  test -x "${prefix}/usr/local/share/nyxveil/scripts/production-gate.sh"
  test -f "${prefix}/usr/local/share/nyxveil/VERSION"
}

prepare_served_fixture() {
  local crlf="${1:-0}"
  FX="$(mktemp -d "${TMPDIR:-/tmp}/nv-fx.XXXXXX")"
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/nv-empty.XXXXXX")"
  SRV="$(mktemp -d "${TMPDIR:-/tmp}/nv-srv.XXXXXX")"
  PREFIX="$(mktemp -d "${TMPDIR:-/tmp}/nv-prefix.XXXXXX")"
  PORT_FILE="${SRV}/.port"
  build_fixture_assets "${FX}" "1.1.3"
  # Placeholder manifests so files exist; rewrite after HTTP base known.
  make_fixture_manifests "${FX}" "1.1.3" "http://127.0.0.1:9"
  write_sums "${FX}" 0
  cp -a "${FX}/." "${SRV}/"
  PID="$(start_http "${SRV}" "${PORT_FILE}")"
  wait_port "${PORT_FILE}"
  BASE="http://127.0.0.1:$(tr -d '[:space:]' < "${PORT_FILE}")"
  make_fixture_manifests "${SRV}" "1.1.3" "${BASE}"
  write_sums "${SRV}" "${crlf}"
  cleanup() {
    kill "${PID}" 2>/dev/null || true
    wait "${PID}" 2>/dev/null || true
    rm -rf "${FX}" "${WORK}" "${SRV}" "${PREFIX}"
  }
  trap cleanup EXIT
}

seed_old_install() {
  mkdir -p "${PREFIX}/usr/local/sbin" "${PREFIX}/usr/local/share/nyxveil/scripts" "${PREFIX}/var/lib/nyxveil"
  printf 'old-server-1.1.1\n' > "${PREFIX}/usr/local/sbin/nyxveil-server"
  printf 'old-ctl-1.1.1\n' > "${PREFIX}/usr/local/sbin/nyxveilctl"
  chmod 0755 "${PREFIX}/usr/local/sbin/nyxveil-server" "${PREFIX}/usr/local/sbin/nyxveilctl"
  [[ ! -e "${PREFIX}/usr/local/sbin/nyxveil-catalog-verify" ]]
  [[ ! -e "${PREFIX}/usr/local/share/nyxveil/scripts/production-gate.sh" ]]
}

place_only_live_final() {
  cp -a "${ROOT}/scripts/live-final-update.sh" "${WORK}/live-final-update.sh"
  chmod 0755 "${WORK}/live-final-update.sh"
  mapfile -t entries < <(ls -A "${WORK}")
  [[ "${#entries[@]}" -eq 1 && "${entries[0]}" == "live-final-update.sh" ]] || \
    die "workdir must contain only live-final-update.sh (got: ${entries[*]})"
}

run_empty_dir_full() {
  local crlf="${1:-0}"
  prepare_served_fixture "${crlf}"
  seed_old_install
  place_only_live_final
  (
    cd "${WORK}"
    NYXVEIL_SKIP_ROOT=1 \
    NYXVEIL_SKIP_GATE=1 \
    NYXVEIL_BIN_DIR="${PREFIX}/usr/local/sbin" \
    NYXVEIL_SHARE_DIR="${PREFIX}/usr/local/share/nyxveil" \
    NYXVEIL_STATE_DIR="${PREFIX}/var/lib/nyxveil" \
      ./live-final-update.sh --base-url "${BASE}"
  )
  assert_install_complete "${PREFIX}"
  grep -q '1.1.3' "${PREFIX}/usr/local/share/nyxveil/VERSION"
  echo "RESULT=PASS"
}

case "${CASE}" in
  TestLiveFinalUpdateEmptyWorkingDirectory|TestLiveFinalUpdateHandlesLFChecksums)
    need_jq; need_go
    run_empty_dir_full 0
    ;;
  TestLiveFinalUpdateHandlesCRLFChecksums)
    need_jq; need_go
    run_empty_dir_full 1
    ;;
  TestLiveFinalUpdateDownloadsVersion|TestLiveFinalUpdateDownloadsBootstrap)
    need_jq; need_go
    prepare_served_fixture 0
    place_only_live_final
    (
      cd "${WORK}"
      NYXVEIL_SKIP_ROOT=1 ./live-final-update.sh --base-url "${BASE}" --verify-chain
    )
    [[ ! -f "${WORK}/VERSION" ]] || die "VERSION leaked into caller cwd"
    [[ ! -f "${WORK}/bootstrap-cli-update.sh" ]] || die "bootstrap leaked into caller cwd"
    echo "LIVE_FINAL_UPDATE_CONSUMER=PASS"
    ;;
  TestBootstrapChecksumLF|TestBootstrapChecksumCRLF)
    sums="$(mktemp)"
    f="$(mktemp)"
    echo bootstrap-body > "${f}"
    sum="$(sha256sum "${f}" | awk '{print $1}')"
    name="$(basename "${f}")"
    if [[ "${CASE}" == "TestBootstrapChecksumCRLF" ]]; then
      printf '%s  %s\r\n' "${sum}" "${name}" > "${sums}"
    else
      printf '%s  %s\n' "${sum}" "${name}" > "${sums}"
    fi
    (
      cd "$(dirname "${f}")"
      tr -d '\r' < "${sums}" | grep -E " [*]?${name}\$" | sha256sum -c - >/dev/null
    )
    rm -f "${sums}" "${f}"
    echo "RESULT=PASS"
    ;;
  TestMissingVersionFailsBeforeModification)
    need_jq
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/nv-empty.XXXXXX")"
    SRV="$(mktemp -d "${TMPDIR:-/tmp}/nv-srv.XXXXXX")"
    PREFIX="$(mktemp -d "${TMPDIR:-/tmp}/nv-prefix.XXXXXX")"
    PORT_FILE="${SRV}/.port"
    mkdir -p "${PREFIX}/usr/local/sbin"
    echo old-ctl > "${PREFIX}/usr/local/sbin/nyxveilctl"
    chmod 0755 "${PREFIX}/usr/local/sbin/nyxveilctl"
    echo '{}' > "${SRV}/release-manifest-linux-amd64.json"
    PID="$(start_http "${SRV}" "${PORT_FILE}")"
    trap 'kill "'"${PID}"'" 2>/dev/null || true; rm -rf "'"${WORK}"'" "'"${SRV}"'" "'"${PREFIX}"'"' EXIT
    wait_port "${PORT_FILE}"
    BASE="http://127.0.0.1:$(tr -d '[:space:]' < "${PORT_FILE}")"
    place_only_live_final
    if (
      cd "${WORK}"
      NYXVEIL_SKIP_ROOT=1 \
      NYXVEIL_BIN_DIR="${PREFIX}/usr/local/sbin" \
        ./live-final-update.sh --base-url "${BASE}"
    ); then
      die "expected failure when VERSION missing"
    fi
    grep -q 'old-ctl' "${PREFIX}/usr/local/sbin/nyxveilctl"
    echo "RESULT=PASS"
    ;;
  TestMissingBootstrapFailsBeforeModification)
    need_jq; need_go
    prepare_served_fixture 0
    seed_old_install
    rm -f "${SRV}/bootstrap-cli-update.sh"
    place_only_live_final
    if (
      cd "${WORK}"
      NYXVEIL_SKIP_ROOT=1 \
      NYXVEIL_BIN_DIR="${PREFIX}/usr/local/sbin" \
      NYXVEIL_SHARE_DIR="${PREFIX}/usr/local/share/nyxveil" \
      NYXVEIL_STATE_DIR="${PREFIX}/var/lib/nyxveil" \
        ./live-final-update.sh --base-url "${BASE}"
    ); then
      die "expected failure when bootstrap missing"
    fi
    grep -q 'old-ctl-1.1.1' "${PREFIX}/usr/local/sbin/nyxveilctl"
    echo "RESULT=PASS"
    ;;
  TestTamperedBootstrapFails)
    need_jq; need_go
    prepare_served_fixture 0
    seed_old_install
    printf '%s\n' '#!/bin/bash' 'echo evil-no-pubkey' > "${SRV}/bootstrap-cli-update.sh"
    chmod 0755 "${SRV}/bootstrap-cli-update.sh"
    # Keep the trusted checksum unchanged: corrupt bytes must fail integrity verification.
    place_only_live_final
    if (
      cd "${WORK}"
      NYXVEIL_SKIP_ROOT=1 \
      NYXVEIL_BIN_DIR="${PREFIX}/usr/local/sbin" \
        ./live-final-update.sh --base-url "${BASE}" --verify-chain
    ); then
      die "expected tampered bootstrap failure"
    fi
    grep -q 'old-ctl-1.1.1' "${PREFIX}/usr/local/sbin/nyxveilctl"
    echo "RESULT=PASS"
    ;;
  TestTamperedCtlFails)
    need_jq; need_go
    prepare_served_fixture 0
    seed_old_install
    arch="$(tr -d '[:space:]' < "${SRV}/.arch")"
    echo tampered-ctl > "${SRV}/nyxveilctl-linux-${arch}"
    place_only_live_final
    if (
      cd "${WORK}"
      NYXVEIL_SKIP_ROOT=1 \
      NYXVEIL_BIN_DIR="${PREFIX}/usr/local/sbin" \
      NYXVEIL_SHARE_DIR="${PREFIX}/usr/local/share/nyxveil" \
      NYXVEIL_STATE_DIR="${PREFIX}/var/lib/nyxveil" \
        ./live-final-update.sh --base-url "${BASE}"
    ); then
      die "expected tampered ctl failure"
    fi
    grep -q 'old-ctl-1.1.1' "${PREFIX}/usr/local/sbin/nyxveilctl"
    echo "RESULT=PASS"
    ;;
  TestLiveFinalTrustUsesGitHubReleaseModel)
    grep -q 'SHA256SUMS' "${ROOT}/scripts/live-final-update.sh"
    grep -q 'GitHub' "${ROOT}/scripts/live-final-update.sh"
    grep -q 'sha256' "${ROOT}/scripts/bootstrap-cli-update.sh"
    ! grep -q 'PUB_HEX' "${ROOT}/scripts/live-final-update.sh"
    ! grep -q 'verify_manifest_signature' "${ROOT}/scripts/live-final-update.sh"
    ! grep -q 'UpdatePublicKey' "${ROOT}/internal/updater/updater.go"
    echo "RESULT=PASS"
    ;;
  *)
    die "unknown case"
    ;;
esac
