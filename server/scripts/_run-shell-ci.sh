#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FAIL=0
for script in scripts/test-*.sh
do
  s="$(basename "${script}" .sh)"
  echo "== ${s} =="
  if [[ "${s}" == test-live-final-cases ]]; then
    while IFS= read -r scenario; do
      if ! timeout -k 5 180 bash "${script}" "${scenario}"; then echo "FAIL ${s}/${scenario}"; FAIL=1; fi
    done < <(sed -n 's/^  \(Test[^)]*\))$/\1/p' "${script}" | tr '|' '\n')
    continue
  fi
  if timeout -k 5 240 bash "scripts/${s}.sh"; then
    echo "SCRIPT_EXIT_OK ${s} (gate status is reported above; SKIP is not PASS)"
  else
    echo "FAIL ${s}"
    FAIL=1
  fi
done
exit "${FAIL}"
