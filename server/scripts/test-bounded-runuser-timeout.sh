#!/usr/bin/env bash
# Regression: runuser/timeout wrapper pattern used by install.sh run_as_nyxveil_bounded.
# Does NOT call install.sh register. Requires Linux + coreutils timeout.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${ROOT}/installer/install.sh"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "SKIP non-Linux host"
  exit 0
fi

command -v timeout >/dev/null 2>&1 || { echo "SKIP timeout not available"; exit 0; }

[[ -f "${INSTALLER}" ]] || { echo "missing ${INSTALLER}" >&2; exit 1; }

echo "== static install.sh wrapper contract =="
if grep -E 'timeout[[:space:]].*run_as_nyxveil([^_]|$)' "${INSTALLER}" >/dev/null 2>&1; then
  fail "install.sh still wraps shell function with timeout (timeout … run_as_nyxveil)"
else
  pass "no timeout … run_as_nyxveil anti-pattern"
fi

if grep -q 'run_as_nyxveil_bounded' "${INSTALLER}"; then
  pass "run_as_nyxveil_bounded present"
else
  fail "run_as_nyxveil_bounded missing"
fi

# Prefer the install.sh pattern: runuser -u USER -- timeout -k N SEC exe …
# Fall back to timeout wrapping current-user binary when runuser is absent.
TMP="$(mktemp -d /tmp/nyxveil-bounded-timeout.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

cat >"${TMP}/echo_stdin.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
IFS= read -r line || true
printf 'stdin=%s\n' "${line}"
exit 0
EOF
chmod +x "${TMP}/echo_stdin.sh"

cat >"${TMP}/exit_code.sh" <<'EOF'
#!/usr/bin/env bash
exit 42
EOF
chmod +x "${TMP}/exit_code.sh"

cat >"${TMP}/hang.sh" <<'EOF'
#!/usr/bin/env bash
exec sleep 30
EOF
chmod +x "${TMP}/hang.sh"

run_bounded() {
  local sec="$1" kill_after="$2"
  shift 2
  local exe="$1"
  shift
  if command -v runuser >/dev/null 2>&1; then
    # Same shape as install.sh: user switch outer, timeout wraps real binary.
    runuser -u "$(id -un)" -- timeout -k "${kill_after}" "${sec}" "${exe}" "$@"
    return $?
  fi
  timeout -k "${kill_after}" "${sec}" "${exe}" "$@"
  return $?
}

echo "== stdin reaches wrapped binary =="
out="$(printf 'tok-secret\n' | run_bounded 5 1 "${TMP}/echo_stdin.sh" || true)"
if [[ "${out}" == "stdin=tok-secret" ]]; then
  pass "stdin preserved through timeout wrapper"
else
  fail "stdin not preserved (got '${out}')"
fi

echo "== exit code propagated =="
set +e
run_bounded 5 1 "${TMP}/exit_code.sh"
rc=$?
set -e
if [[ "${rc}" -eq 42 ]]; then
  pass "exit code 42 propagated"
else
  fail "expected exit 42 got ${rc}"
fi

echo "== timeout kills hung process (exit 124) =="
set +e
run_bounded 2 1 "${TMP}/hang.sh"
rc=$?
set -e
if [[ "${rc}" -eq 124 ]]; then
  pass "timeout exit 124 on hung sleep"
else
  fail "expected timeout exit 124 got ${rc}"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-bounded-runuser-timeout FAILED" >&2
  exit 1
fi
echo "test-bounded-runuser-timeout PASSED"
