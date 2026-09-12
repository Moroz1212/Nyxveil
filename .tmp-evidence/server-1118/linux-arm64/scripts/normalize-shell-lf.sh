#!/usr/bin/env bash
# normalize-shell-lf.sh — rewrite PATH(s) to Unix LF (strip CR). Idempotent.
set -euo pipefail

die() { echo "normalize-shell-lf: $*" >&2; exit 1; }

normalize_file() {
  local f="$1"
  [[ -f "${f}" ]] || die "missing ${f}"
  local tmp
  tmp="$(mktemp)"
  # tr -d '\r' preserves content; no BOM introduced.
  tr -d '\r' < "${f}" > "${tmp}"
  # Preserve mode best-effort.
  local mode
  mode="$(stat -c '%a' "${f}" 2>/dev/null || stat -f '%OLp' "${f}" 2>/dev/null || echo 755)"
  mv -f "${tmp}" "${f}"
  chmod "${mode}" "${f}" 2>/dev/null || chmod 0755 "${f}"
}

[[ $# -gt 0 ]] || die "usage: normalize-shell-lf.sh PATH [PATH...]"

for p in "$@"; do
  if [[ -d "${p}" ]]; then
    while IFS= read -r -d '' f; do
      normalize_file "${f}"
    done < <(find "${p}" -type f \( -name '*.sh' -o -name '*.service' -o -name '*.conf' \) -print0)
  else
    normalize_file "${p}"
  fi
done

echo "normalize-shell-lf: OK"
