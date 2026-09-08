# Build (Android)

## Prerequisites

- JDK 17 (`JAVA_HOME`, or `clients/android/tools/jdk-17`, or `%USERPROFILE%\tools\jdk-17`)
- Android SDK (`ANDROID_HOME` / `ANDROID_SDK_ROOT`, or `C:\Android\Sdk`, or `%LOCALAPPDATA%\Android\Sdk`)
- Go 1.24+ on PATH (gomobile bind)
- **Pinned NDK: r26b / `26.1.10909125`** (AGP 8.7 + gomobile)

### NDK path-length (Windows)

Do **not** keep a multi‑gigabyte NDK inside the Git tree. Prefer a short external toolchain path:

| Priority | Location |
|----------|----------|
| 1 | `ANDROID_NDK_HOME` / `ANDROID_NDK_ROOT` (must be usable `clang.exe`) |
| 2 | `C:\NvT\ndk` |
| 3 | `%LOCALAPPDATA%\Nyxveil\Toolchains\ndk-26.1.10909125` |
| 4 | `C:\NyxveilToolchain\ndk-26.1.10909125` |
| 5 | `%ANDROID_HOME%\ndk\26.1.10909125` |

`scripts/build-native.ps1` logs:

```
NDK version = r26b (26.1.10909125)
NDK path = ...
```

Install example:

```text
sdkmanager "ndk;26.1.10909125"
```

If the SDK path is too long for NDK extraction on Windows, extract/copy that NDK revision to `C:\NvT\ndk` and set `ANDROID_NDK_HOME=C:\NvT\ndk`.

## One-command build (includes gomobile)

```powershell
cd clients\android
.\scripts\build-apk.ps1
# optional install:
.\scripts\build-apk.ps1 -Install
```

Pipeline:

1. Assert Frozen Core provenance (`clients/windows/scripts/assert-frozen-core.ps1`)
2. `scripts/build-native.ps1` — `go test` → `gomobile bind` → AAR validation
3. Gradle unit tests + `assembleDebug`
4. APK native gate (`lib/arm64-v8a/*.so`)
5. Copy to `dist/Nyxveil-Android-v{VERSION}-debug.apk` + SHA256

Native-only:

```powershell
.\scripts\build-native.ps1
```

Generated artifacts (safe to delete; rebuilt by scripts):

- `bridge/native/nyxveilbridge.aar`
- `bridge/build/`, `app/build/`, `.gradle/`, `dist/*.apk`

## Linux / macOS

```bash
cd clients/android
./scripts/build-apk.sh
```

## Manual Gradle

Requires an existing AAR from `build-native.ps1`:

```bash
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

Control Plane URL is compile-time only (`BuildConfig.CONTROL_PLANE_BASE_URL`) and must never appear in UI.

## ABI

First testable APK ships **arm64-v8a** (`libgojni.so` via gomobile).
