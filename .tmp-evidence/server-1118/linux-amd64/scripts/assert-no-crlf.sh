#!/usr/bin/env bash
# assert-no-crlf.sh — fail if any listed path (or tree of *.sh) contains byte 0x0D.
set -euo pipefail

die() { echo "assert-no-crlf: $*" >&2; exit 1; }

# Exit 0 => file CONTAINS CR; exit 1 => clean LF.
file_has_cr() {
  local f="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import sys; data=open(sys.argv[1],"rb").read(); sys.exit(0 if b"\r" in data else 1)' "${f}"
    return $?
  fi
  # grep exit 0 = match found = has CR
  grep -l $'\r' "${f}" >/dev/null 2>&1
}

scan_file() {
  local f="$1"
  [[ -f "${f}" ]] || die "missing ${f}"
  if file_has_cr "${f}"; then
    die "CRLF/CR (byte 0x0d) detected in ${f}"
  fi
}

[[ $# -gt 0 ]] || die "usage: assert-no-crlf.sh PATH [PATH...]"

for p in "$@"; do
  if [[ -d "${p}" ]]; then
    while IFS= read -r -d '' f; do
      scan_file "${f}"
    done < <(find "${p}" -type f -name '*.sh' -print0)
  else
    scan_file "${p}"
  fi
done

echo "assert-no-crlf: OK ($# path(s))"
