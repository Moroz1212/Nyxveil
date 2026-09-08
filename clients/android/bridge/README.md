# Nyxveil Android Go bridge

This module exposes NVP Frozen Core to the Android app via **gomobile bind**.

## Layout

- `src/main/java/ru/nyxveil/bridge/NvpBridge.kt` — Kotlin façade over gomobile `nyxveilbridge`
- `go/` — Go package `nyxveilbridge` (source of truth)
- `native/nyxveilbridge.aar` — **generated** by `scripts/build-native.ps1` (not committed)

## Build

Prefer the one-command APK build (runs native automatically):

```powershell
cd clients\android
.\scripts\build-apk.ps1
```

Native-only:

```powershell
.\scripts\build-native.ps1
```

Pinned NDK: **r26b (`26.1.10909125`)**. See `docs/BUILD.md`.

## Replace path

`go.mod` uses:

```
replace github.com/nyxveil/nvp => ../../../windows/third_party/nvp
```

Do **not** copy NVP into `clients/android`; the Windows third_party tree is the source of truth.

## TUN FD ownership

Kotlin: `ParcelFileDescriptor.detachFd()` then pass the int to `Engine.attachTun`.
Go owns and closes the fd on `Disconnect`. Kotlin must not close the detached fd again.

## Verify Go package

```bash
cd clients/android/bridge/go
go test ./...
```
