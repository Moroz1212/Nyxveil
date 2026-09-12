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

# Production shape: transient CAP_NET_BIND_SERVICE via systemd-run or setpriv;
# timeout wraps the REAL executable; never uncapped runuser for bounded register.
if grep -q 'AmbientCapabilities=CAP_NET_BIND_SERVICE' "${INSTALLER}"; then
  pass "systemd-run AmbientCapabilities for registration"
else
  fail "missing AmbientCapabilities registration path"
fi
if grep -qE 'runuser -u nyxveil -- timeout -k' "${INSTALLER}"; then
  fail "uncapped runuser+timeout still used for bounded registration"
else
  pass "no uncapped runuser+timeout for bounded registration"
fi
if grep -qF -- 'setpriv --reuid=nyxveil' "${INSTALLER}"; then
  pass "setpriv fallback present"
else
  fail "missing setpriv fallback"
fi
for property in \
  '--bounding-set=-all,+net_bind_service' \
  '--no-new-privs' \
  '--inh-caps=-all,+net_bind_service' \
  '--ambient-caps=-all,+net_bind_service'; do
  if grep -qF -- "${property}" "${INSTALLER}"; then
    pass "setpriv fallback ${property}"
  else
    fail "missing setpriv fallback ${property}"
  fi
done

TMP="$(mktemp -d /tmp/nyxveil-bounded-timeout.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

cat >"${TMP}/echo_stdin.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
IFS= read -r line || true
printf 'stdin=%s\n' "${line}"
whoami_out="$(id -un 2>/dev/null || true)"
printf 'user=%s\n' "${whoami_out}"
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
# Default signal disposition: timeout sends TERM then KILL; expect exit 124.
exec sleep 30
EOF
chmod +x "${TMP}/hang.sh"

# Exercise the exact wrapper shape when root; otherwise prove timeout wraps a real
# executable (CI runners are non-root and cannot runuser).
run_bounded() {
  local sec="$1" kill_after="$2"
  shift 2
  local exe="$1"
  shift
  if [[ "${EUID}" -eq 0 ]] && command -v runuser >/dev/null 2>&1; then
    runuser -u "$(id -un)" -- timeout -k "${kill_after}" "${sec}" "${exe}" "$@"
    return $?
  fi
  if [[ "${EUID}" -eq 0 ]] && command -v setpriv >/dev/null 2>&1; then
    setpriv --reuid="$(id -u)" --regid="$(id -g)" --clear-groups -- \
      timeout -k "${kill_after}" "${sec}" "${exe}" "$@"
    return $?
  fi
  # Non-root CI: timeout must wrap the actual executable (not a shell function).
  timeout -k "${kill_after}" "${sec}" "${exe}" "$@"
  return $?
}

echo "== stdin reaches wrapped binary =="
out="$(printf 'tok-secret\n' | run_bounded 5 1 "${TMP}/echo_stdin.sh" || true)"
if printf '%s\n' "${out}" | grep -qx 'stdin=tok-secret'; then
  pass "stdin preserved through timeout wrapper"
else
  fail "stdin not preserved (got '${out}')"
fi
if printf '%s\n' "${out}" | grep -q '^user='; then
  pass "command started (user line present)"
else
  fail "wrapped command did not report user"
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

echo "== timeout kills hung process (exit 124 or 137) =="
set +e
run_bounded 2 1 "${TMP}/hang.sh"
rc=$?
set -e
# 124 = timeout; 137 = 128+9 KILL if TERM ignored / race on some runners.
if [[ "${rc}" -eq 124 || "${rc}" -eq 137 ]]; then
  pass "hung process terminated (rc=${rc})"
else
  fail "expected timeout kill exit 124/137 got ${rc}"
fi

# Prove TERM is delivered: short timeout against a process that exits on TERM.
cat >"${TMP}/term_ok.sh" <<'EOF'
#!/usr/bin/env bash
trap 'exit 77' TERM
sleep 30
exit 0
EOF
chmod +x "${TMP}/term_ok.sh"
echo "== TERM delivered to child =="
set +e
run_bounded 1 5 "${TMP}/term_ok.sh"
rc=$?
set -e
# timeout returns 124 when it kills; child may exit 77 on TERM before timeout reports.
# Accept 124 (timeout) or 77 (child handled TERM) — both prove TERM path works.
if [[ "${rc}" -eq 124 || "${rc}" -eq 77 ]]; then
  pass "TERM path exercised (rc=${rc})"
else
  fail "expected TERM-related exit 124/77 got ${rc}"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-bounded-runuser-timeout FAILED" >&2
  exit 1
fi
echo "test-bounded-runuser-timeout PASSED"
