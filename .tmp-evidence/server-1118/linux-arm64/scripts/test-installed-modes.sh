#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
eval "$(sed -n '/^assert_installed_mode()/,/^}/p' "${ROOT}/installer/install.sh")"
die() { echo "$*" >&2; exit 1; }
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
printf 'plain mock asset\n' >"${TMP}/asset"
uname() { echo "${PLATFORM}"; }
stat() { echo 644; }
MOCK=1 PLATFORM=MINGW64_NT
assert_installed_mode "${TMP}/asset" 0755
if (assert_installed_mode "${TMP}/missing" 0755) 2>/dev/null; then exit 1; fi
# No Unix-mode bypass for production, even if a platform reports Windows.
MOCK=0
if (assert_installed_mode "${TMP}/asset" 0755) 2>/dev/null; then exit 1; fi
# Linux mocks enforce the same modes as production.
MOCK=1 PLATFORM=Linux
if (assert_installed_mode "${TMP}/asset" 0755) 2>/dev/null; then exit 1; fi
assert_installed_mode "${TMP}/asset" 0644
stat() { echo 755; }
if (assert_installed_mode "${TMP}/asset" 0644) 2>/dev/null; then exit 1; fi
echo 'INSTALLED_MODE_CONTRACT=PASS (Windows mock isolated; Linux/production strict)'
