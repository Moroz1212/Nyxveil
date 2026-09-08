#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
./gradlew clean || true
rm -rf app/build bridge/build build .gradle dist
echo "Clean complete."
