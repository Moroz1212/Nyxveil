#!/usr/bin/env bash
# assert-frozen-core.sh — fail build/package if Frozen Core provenance drifts.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPECTED="7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b"
DOC="${ROOT}/THIRD_PARTY_CORE.md"
NVP="${ROOT}/third_party/nvp"

die() { echo "assert-frozen-core: $*" >&2; exit 1; }

[[ -f "${DOC}" ]] || die "missing THIRD_PARTY_CORE.md"
grep -q "${EXPECTED}" "${DOC}" || die "THIRD_PARTY_CORE.md missing expected SHA ${EXPECTED}"

[[ -d "${NVP}" ]] || die "missing third_party/nvp"
[[ -f "${NVP}/go.mod" ]] || die "third_party/nvp/go.mod missing"

# Prefer verifying against the authoritative zip when present next to the monorepo.
ZIP_CANDIDATES=(
  "${ROOT}/../Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip"
  "${ROOT}/Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip"
)
for z in "${ZIP_CANDIDATES[@]}"; do
  if [[ -f "${z}" ]]; then
    got="$(sha256sum "${z}" | awk '{print $1}')"
    [[ "${got}" == "${EXPECTED}" ]] || die "Frozen Core zip SHA mismatch for ${z}: ${got}"
    echo "assert-frozen-core: zip OK ${z}"
    break
  fi
done

# Vendored tree must expose sendControl (linkname shim target) and TypeConfig.
grep -q 'func (s \*Session) sendControl' "${NVP}/core/session/session.go" \
  || die "Frozen Core missing Session.sendControl (linkname target)"
grep -q 'TypeConfig' "${NVP}/core/control/messages.go" \
  || die "Frozen Core missing TypeConfig"

echo "assert-frozen-core: OK ${EXPECTED}"
