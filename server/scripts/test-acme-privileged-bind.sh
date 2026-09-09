#!/usr/bin/env bash
# ACME_PRIVILEGED_BIND + BOOTSTRAP_SECRET_HYGIENE contract for registration wrapper.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${ROOT}/installer/install.sh"
FAIL=0

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

[[ -f "${INSTALLER}" ]] || { echo "missing installer" >&2; exit 1; }

echo "== static wrapper contract =="
grep -q 'AmbientCapabilities=CAP_NET_BIND_SERVICE' "${INSTALLER}" \
  && pass "systemd-run AmbientCapabilities=CAP_NET_BIND_SERVICE" \
  || fail "missing systemd-run AmbientCapabilities"
grep -q 'ambient-caps=+net_bind_service' "${INSTALLER}" \
  && pass "setpriv ambient-caps fallback" \
  || fail "missing setpriv ambient-caps"
# Comments may mention setcap; forbid actual invocations as commands.
if grep -E '(^|[[:space:];|&])(setcap|/sbin/setcap|/usr/sbin/setcap)[[:space:]]+[^[:space:]]' "${INSTALLER}" \
  | grep -vE '^[[:space:]]*#' >/dev/null; then
  fail "installer must not invoke persistent setcap"
else
  pass "no persistent setcap invocation"
fi
if grep -E '(^|[[:space:];|&])sysctl[[:space:]].*ip_unprivileged_port_start' "${INSTALLER}" \
  | grep -vE '^[[:space:]]*#' >/dev/null \
  || grep -E '^[[:space:]]*[^#]*ip_unprivileged_port_start[[:space:]]*=' "${INSTALLER}" >/dev/null; then
  fail "installer must not weaken ip_unprivileged_port_start"
else
  pass "no global sysctl port weaken"
fi
grep -qE 'runuser -u nyxveil -- timeout -k' "${INSTALLER}" \
  && fail "bounded registration still uses uncapped runuser+timeout" \
  || pass "bounded path does not use uncapped runuser+timeout"
grep -q 'printf.*BOOTSTRAP_TOKEN' "${INSTALLER}" && grep -q 'run_as_nyxveil_bounded' "${INSTALLER}" \
  && pass "bootstrap token piped into bounded wrapper" \
  || fail "bootstrap token stdin handoff missing"

TMP="$(mktemp -d /tmp/nyxveil-acme-bind.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

cat >"${TMP}/inspect_argv.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'argc=%s\n' "$#"
for a in "$@"; do printf 'arg=%s\n' "${a}"; done
env | grep -E 'BOOTSTRAP|TOKEN|NYXVEIL_BOOTSTRAP' || true
IFS= read -r line || true
printf 'stdin_len=%s\n' "${#line}"
exit 0
EOF
chmod +x "${TMP}/inspect_argv.sh"

MOCK=1
# shellcheck disable=SC1090
eval "$(sed -n '/^run_as_nyxveil_bounded()/,/^}/p' "${INSTALLER}")"

echo "== MOCK bounded wrapper preserves stdin, no token on argv =="
SECRET='tok-live-secret-do-not-leak'
out="$(printf '%s\n' "${SECRET}" | run_as_nyxveil_bounded 5 1 "${TMP}/inspect_argv.sh" --register-stdin --config /tmp/x 2>&1 || true)"
echo "${out}" | grep -q "stdin_len=${#SECRET}" && pass "stdin delivered" || fail "stdin missing"
echo "${out}" | grep -F "${SECRET}" >/dev/null && fail "secret leaked in output" || pass "secret absent from output"
echo "${out}" | grep -q 'arg=--register-stdin' && pass "register-stdin present" || fail "register-stdin missing"

if [[ "$(uname -s)" != "Linux" || "${EUID}" -ne 0 ]]; then
  echo "ACME_PRIVILEGED_BIND=PASS (static+mock; live bind skipped)"
  echo "BOOTSTRAP_SECRET_HYGIENE=PASS"
  [[ "${FAIL}" -eq 0 ]] || exit 1
  exit 0
fi

START="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
if [[ "${START}" -le 80 ]]; then
  fail "ip_unprivileged_port_start=${START} already allows privileged ports"
else
  pass "ip_unprivileged_port_start=${START} (>80)"
fi

if id -u nyxveil >/dev/null 2>&1; then
  got="$(runuser -u nyxveil -- python3 -c 'import socket; s=socket.socket()
try:
  s.bind(("127.0.0.1", 80)); print("BOUND")
except PermissionError:
  print("DENIED")
except OSError as e:
  print("OSERR")' || true)"
  [[ "${got}" == "DENIED" ]] && pass "uncapped nyxveil cannot bind :80" \
    || fail "uncapped bind expected DENIED got '${got}'"

  cat >"${TMP}/bind80.py" <<'PY'
import socket, os, sys
s = socket.socket()
try:
    s.bind(("127.0.0.1", 80))
    print("BIND_OK uid=%s" % os.getuid())
    sys.exit(0)
except Exception as e:
    print("BIND_FAIL %s" % e)
    sys.exit(1)
PY
  MOCK=0
  eval "$(sed -n '/^run_as_nyxveil_bounded()/,/^}/p' "${INSTALLER}")"
  if run_as_nyxveil_bounded 10 2 /usr/bin/python3 "${TMP}/bind80.py"; then
    pass "transient CAP_NET_BIND_SERVICE allows :80 as nyxveil"
  else
    fail "privileged bind :80 failed under registration wrapper"
  fi
  if command -v getcap >/dev/null 2>&1; then
    caps="$(getcap /usr/bin/python3 2>/dev/null || true)"
    [[ -z "${caps}" ]] && pass "no persistent file capability on probe binary" \
      || fail "unexpected file caps: ${caps}"
  fi
  if command -v getcap >/dev/null 2>&1 && [[ -x /usr/local/sbin/nyxveil-server ]]; then
    scaps="$(getcap /usr/local/sbin/nyxveil-server 2>/dev/null || true)"
    [[ -z "${scaps}" ]] && pass "nyxveil-server has no persistent file caps" \
      || fail "nyxveil-server has file caps: ${scaps}"
  fi
else
  pass "skip live bind (no nyxveil user)"
fi

NOW="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
[[ "${NOW}" == "${START}" ]] && pass "sysctl unchanged (${NOW})" || fail "sysctl changed ${START}->${NOW}"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "ACME_PRIVILEGED_BIND=FAIL"
  echo "BOOTSTRAP_SECRET_HYGIENE=FAIL"
  exit 1
fi
echo "ACME_PRIVILEGED_BIND=PASS"
echo "BOOTSTRAP_SECRET_HYGIENE=PASS"
exit 0
