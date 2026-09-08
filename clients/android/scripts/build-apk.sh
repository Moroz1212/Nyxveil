#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ ! -f settings.gradle.kts ]]; then
  echo "Cannot locate clients/android root" >&2
  exit 1
fi

resolve_java() {
  if [[ -n "${JAVA_HOME:-}" && -x "$JAVA_HOME/bin/java" ]]; then
    echo "$JAVA_HOME"
    return
  fi
  if [[ -x "$ROOT/tools/jdk-17/bin/java" ]]; then
    echo "$ROOT/tools/jdk-17"
    return
  fi
  echo "JAVA_HOME not set and tools/jdk-17 not found" >&2
  exit 1
}

resolve_sdk() {
  for c in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "$HOME/Android/Sdk" "$HOME/Library/Android/sdk"; do
    if [[ -n "$c" && -d "$c" ]]; then
      echo "$c"
      return
    fi
  done
  echo "Android SDK not found. Set ANDROID_HOME or ANDROID_SDK_ROOT." >&2
  exit 1
}

JAVA_HOME="$(resolve_java)"
ANDROID_SDK="$(resolve_sdk)"
export JAVA_HOME ANDROID_HOME="$ANDROID_SDK" ANDROID_SDK_ROOT="$ANDROID_SDK"

printf 'sdk.dir=%s\n' "$ANDROID_SDK" > local.properties

echo "JAVA_HOME=$JAVA_HOME"
echo "ANDROID_SDK=$ANDROID_SDK"

./gradlew :app:testDebugUnitTest :app:assembleDebug

VERSION="$(tr -d '[:space:]' < VERSION)"
APK_SRC="app/build/outputs/apk/debug/app-debug.apk"
mkdir -p dist
APK_DST="dist/Nyxveil-Android-v${VERSION}-debug.apk"
cp -f "$APK_SRC" "$APK_DST"

SIZE="$(wc -c < "$APK_DST" | tr -d ' ')"
if command -v sha256sum >/dev/null 2>&1; then
  HASH="$(sha256sum "$APK_DST" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  HASH="$(shasum -a 256 "$APK_DST" | awk '{print $1}')"
else
  HASH="(sha256 tool missing)"
fi

echo "APK: $APK_DST"
echo "Size: $SIZE bytes"
echo "SHA256: $HASH"

if [[ "${1:-}" == "--install" || "${1:-}" == "-Install" ]]; then
  "$ROOT/scripts/install-apk.sh" "$APK_DST"
fi
