#!/usr/bin/env bash
# Prove install.sh http_get is bounded and never leaves partial assets on failure.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${ROOT}/installer/install.sh"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

[[ -f "${INSTALLER}" ]] || { echo "missing installer" >&2; exit 1; }

# Static contract: bounded curl flags + retries + partial cleanup.
grep -q -- '--connect-timeout' "${INSTALLER}" || fail "missing --connect-timeout"
grep -q -- '--max-time' "${INSTALLER}" || fail "missing --max-time"
grep -q 'NYXVEIL_HTTP_CONNECT_TIMEOUT_SEC' "${INSTALLER}" || fail "missing connect timeout env"
grep -q 'NYXVEIL_HTTP_MAX_TIME_SEC' "${INSTALLER}" || fail "missing max-time env"
grep -q 'NYXVEIL_HTTP_RETRIES' "${INSTALLER}" || fail "missing retries env"
grep -q '\.partial' "${INSTALLER}" || fail "missing .partial staging"
# Unbounded legacy pattern must not remain in http_get body.
if awk '/^http_get\(\)/,/^}/ {print}' "${INSTALLER}" | grep -E 'curl -fsSL -o "\$dest"' >/dev/null; then
  fail "unbounded curl -fsSL -o \$dest still present in http_get"
else
  pass "http_get no unbounded curl -fsSL -o \$dest"
fi

# Runtime fault injection requires a TCP listener that accepts then stalls.
if ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
  echo "SKIP runtime hang tests (no python)"
  [[ "${FAIL}" -eq 0 ]]
  exit 0
fi

PY=python3
command -v python3 >/dev/null 2>&1 || PY=python

TMP="$(mktemp -d /tmp/nyxveil-http-get.XXXXXX)"
cleanup() { rm -rf "${TMP}"; kill "${STALL_PID:-}" 2>/dev/null || true; }
trap cleanup EXIT

# Stall server: accept TCP, optionally send slow/partial headers, never finish body.
cat >"${TMP}/stall.py" <<'PY'
import socket, sys, time, threading
mode = sys.argv[1]
port_file = sys.argv[2]
srv = socket.socket()
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", 0))
srv.listen(5)
host, port = srv.getsockname()
open(port_file, "w", encoding="utf-8").write(str(port))

def handle(conn):
    try:
        conn.settimeout(30)
        if mode == "headers_hang":
            conn.recv(1)
            time.sleep(60)
        elif mode == "body_stall":
            conn.recv(4096)
            conn.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 1048576\r\n\r\n")
            time.sleep(60)
        elif mode == "reset":
            conn.recv(1)
            conn.close()
            return
        elif mode == "http404":
            conn.recv(4096)
            conn.sendall(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
        elif mode == "http500":
            conn.recv(4096)
            conn.sendall(b"HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\n\r\n")
        elif mode == "partial":
            conn.recv(4096)
            conn.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial")
            time.sleep(60)
        else:  # connect hang: never accept more / sleep before accept handled externally
            time.sleep(60)
    except Exception:
        pass
    finally:
        try:
            conn.close()
        except Exception:
            pass

while True:
    try:
        c, _ = srv.accept()
    except Exception:
        break
    threading.Thread(target=handle, args=(c,), daemon=True).start()
PY

run_case() {
  local mode="$1" expect_fail="$2"
  local portf="${TMP}/port.${mode}" dest="${TMP}/out.${mode}"
  rm -f "${portf}" "${dest}" "${dest}.partial"
  "${PY}" "${TMP}/stall.py" "${mode}" "${portf}" &
  STALL_PID=$!
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [[ -f "${portf}" ]] && break
    sleep 0.1
  done
  local port
  port="$(cat "${portf}")"
  # Source only http_get by extracting and evaluating a minimal harness.
  set +e
  (
    export NYXVEIL_HTTP_CONNECT_TIMEOUT_SEC=1
    export NYXVEIL_HTTP_MAX_TIME_SEC=2
    export NYXVEIL_HTTP_RETRIES=1
    MOCK=0
    warn() { :; }
    die() { echo "die: $*" >&2; exit 1; }
    eval "$(awk '/^http_get\(\)/,/^}/ {print}' "${INSTALLER}")"
    http_get "http://127.0.0.1:${port}/asset.bin" "${dest}"
    exit $?
  )
  local rc=$?
  set -e
  kill "${STALL_PID}" 2>/dev/null || true
  wait "${STALL_PID}" 2>/dev/null || true
  STALL_PID=""
  if [[ "${expect_fail}" -eq 1 ]]; then
    if [[ "${rc}" -eq 0 ]]; then
      fail "${mode}: expected failure, got success"
    else
      pass "${mode}: failed closed rc=${rc}"
    fi
    if [[ -f "${dest}" ]]; then
      fail "${mode}: partial dest left behind"
    else
      pass "${mode}: no final dest"
    fi
    if [[ -f "${dest}.partial" ]]; then
      fail "${mode}: .partial left behind"
    else
      pass "${mode}: no .partial residue"
    fi
  else
    if [[ "${rc}" -ne 0 ]]; then
      fail "${mode}: unexpected failure rc=${rc}"
    else
      pass "${mode}: success"
    fi
  fi
}

run_case headers_hang 1
run_case body_stall 1
run_case reset 1
run_case http404 1
run_case http500 1
run_case partial 1

# Connect hang: bind a port that never accept()s by using a blackhole via firewall is hard;
# approximate with max-time against non-listening high port (fails fast) is not hang.
# Use python that binds but never accept — separate process.
cat >"${TMP}/noaccept.py" <<'PY'
import socket, sys, time
port_file = sys.argv[1]
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 0))
# intentionally never listen/accept
open(port_file, "w", encoding="utf-8").write(str(s.getsockname()[1]))
time.sleep(60)
PY
# Without listen(), connect fails immediately — still proves timeout envs are wired.
# Real hang: listen without accept.
cat >"${TMP}/listen_no_accept.py" <<'PY'
import socket, sys, time
port_file = sys.argv[1]
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 0))
s.listen(1)
open(port_file, "w", encoding="utf-8").write(str(s.getsockname()[1]))
time.sleep(60)
PY
portf="${TMP}/port.connect_hang"
rm -f "${portf}"
"${PY}" "${TMP}/listen_no_accept.py" "${portf}" &
STALL_PID=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
  [[ -f "${portf}" ]] && break
  sleep 0.1
done
port="$(cat "${portf}")"
dest="${TMP}/out.connect_hang"
set +e
(
  export NYXVEIL_HTTP_CONNECT_TIMEOUT_SEC=1
  export NYXVEIL_HTTP_MAX_TIME_SEC=2
  export NYXVEIL_HTTP_RETRIES=1
  MOCK=0
  warn() { :; }
  die() { echo "die: $*" >&2; exit 1; }
  eval "$(awk '/^http_get\(\)/,/^}/ {print}' "${INSTALLER}")"
  http_get "http://127.0.0.1:${port}/asset.bin" "${dest}"
  exit $?
)
rc=$?
set -e
kill "${STALL_PID}" 2>/dev/null || true
wait "${STALL_PID}" 2>/dev/null || true
STALL_PID=""
if [[ "${rc}" -eq 0 ]]; then
  fail "connect_hang: expected failure"
else
  pass "connect_hang: failed closed rc=${rc}"
fi
[[ -f "${dest}" ]] && fail "connect_hang: dest left" || pass "connect_hang: no dest"
[[ -f "${dest}.partial" ]] && fail "connect_hang: partial left" || pass "connect_hang: no partial"

[[ "${FAIL}" -eq 0 ]]
echo "test-bounded-http-get PASSED"
