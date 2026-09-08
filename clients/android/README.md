# Nyxveil Android Client

Source of truth for the Nyxveil Android VPN client lives **only** in this directory:

`clients/android/`

## Quick start (Windows)

```powershell
cd clients\android
.\scripts\build-apk.ps1
```

Install on a connected device:

```powershell
.\scripts\install-apk.ps1
```

Build and install:

```powershell
.\scripts\build-apk.ps1 -Install
```

## Output

Debug APK (development):

`clients/android/dist/Nyxveil-Android-v1.0.0-debug.apk`

## Docs

- [BUILD.md](docs/BUILD.md)
- [ARCHITECTURE.md](docs/ARCHITECTURE.md)
- [TESTING.md](docs/TESTING.md)
- [ANDROID_SECURITY.md](docs/ANDROID_SECURITY.md)

## Design rules

- Control Plane URL is **not** user-editable (built-in endpoint).
- First-run activation asks for **license key only**.
- VPN lifetime belongs to `VpnService`, not the Activity.
- Protocol is existing **NVP/1 Frozen Core** via the Android Go bridge.
