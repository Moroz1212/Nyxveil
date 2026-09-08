#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
APK="${1:-$ROOT/dist/Nyxveil-Android-v${VERSION}-debug.apk}"

if [[ ! -f "$APK" ]]; then
  echo "APK not found: $APK" >&2
  exit 1
fi

resolve_adb() {
  for sdk in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "$HOME/Android/Sdk" "$HOME/Library/Android/sdk"; do
    if [[ -n "$sdk" && -x "$sdk/platform-tools/adb" ]]; then
      echo "$sdk/platform-tools/adb"
      return
    fi
  done
  if command -v adb >/dev/null 2>&1; then
    command -v adb
    return
  fi
  echo "adb not found" >&2
  exit 1
}

ADB="$(resolve_adb)"
echo "Installing $APK"
"$ADB" install -r "$APK"
