#!/usr/bin/env bash
# Serve ONLY dist/release over HTTP and run live-final-update.sh --verify-chain
# from an empty working directory that initially contains ONLY that script.
set -euo pipefail
export NYXVEIL_TEST_MODE=1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${1:-${ROOT}/dist/release}"
die() { echo "test-live-final-consumer: $*" >&2; exit 1; }

[[ -d "${DIST}" ]] || die "missing release dir ${DIST}"
[[ -x "${DIST}/live-final-update.sh" ]] || die "missing live-final-update.sh"
command -v python3 >/dev/null 2>&1 || die "python3 required"
command -v curl >/dev/null 2>&1 || die "curl required"

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/nyxveil-consumer-empty.XXXXXX")"
SRVDIR="$(mktemp -d "${TMPDIR:-/tmp}/nyxveil-consumer-srv.XXXXXX")"
cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORKDIR}" "${SRVDIR}"
}
trap cleanup EXIT

# Copy ONLY release flat assets into the server root (no source tree).
cp -a "${DIST}/." "${SRVDIR}/"
# Ensure LF for checksums even if a host tool reintroduced CR.
tr -d '\r' < "${SRVDIR}/SHA256SUMS" > "${SRVDIR}/SHA256SUMS.lf"
mv -f "${SRVDIR}/SHA256SUMS.lf" "${SRVDIR}/SHA256SUMS"

PORT_FILE="${SRVDIR}/.port"
python3 - "${SRVDIR}" "${PORT_FILE}" <<'PY' &
import http.server, socketserver, sys, pathlib
root = pathlib.Path(sys.argv[1])
port_file = pathlib.Path(sys.argv[2])
os_chdir = __import__("os").chdir
os_chdir(root)

class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass

with socketserver.TCPServer(("127.0.0.1", 0), Handler) as httpd:
    port = httpd.server_address[1]
    port_file.write_text(str(port), encoding="ascii")
    httpd.serve_forever()
PY
SERVER_PID=$!

for _ in $(seq 1 50); do
  [[ -f "${PORT_FILE}" ]] && break
  sleep 0.1
done
[[ -f "${PORT_FILE}" ]] || die "HTTP server failed to start"
PORT="$(tr -d '[:space:]' < "${PORT_FILE}")"
BASE="http://127.0.0.1:${PORT}"

# Empty working directory initially contains ONLY live-final-update.sh.
cp -a "${DIST}/live-final-update.sh" "${WORKDIR}/live-final-update.sh"
chmod 0755 "${WORKDIR}/live-final-update.sh"
# Assert emptiness except the script.
mapfile -t entries < <(ls -A "${WORKDIR}")
[[ "${#entries[@]}" -eq 1 && "${entries[0]}" == "live-final-update.sh" ]] || \
  die "workdir must contain only live-final-update.sh (got: ${entries[*]})"

# Soft outer integrity (CRLF-safe) then Ed25519 verify-chain inside the script.
(
  cd "${WORKDIR}"
  curl -fsSLO "${BASE}/SHA256SUMS"
  tr -d '\r' < SHA256SUMS | grep -E ' [*]?live-final-update.sh$' | sha256sum -c -
  rm -f SHA256SUMS
  # After removing SHA256SUMS, workdir again has only the script before execution.
  mapfile -t entries < <(ls -A "${WORKDIR}")
  [[ "${#entries[@]}" -eq 1 && "${entries[0]}" == "live-final-update.sh" ]] || \
    die "pre-exec workdir pollution: ${entries[*]}"
  NYXVEIL_SKIP_ROOT=1 \
    ./live-final-update.sh --base-url "${BASE}" --verify-chain
)

echo "test-live-final-consumer: OK"
