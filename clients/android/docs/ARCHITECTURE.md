# Architecture (Android)

## Modules

| Module | Role |
|--------|------|
| `:app` | UI (Compose), licensing, catalog verify, `VpnService`, settings |
| `:bridge` | Kotlin façade over gomobile AAR (`nyxveilbridge`) + Go engine |

## Session flow

1. License key → validate + device activate (platform=`android`). Permanent license stays in the app layer only.
2. Catalog keys + signed catalog → Ed25519 verify + temporal check.
3. UI selects a **location** (not a physical node).
4. Ticket issue (short-lived access ticket only → native engine).
5. **Begin** (native): protected dial (QUIC/TLS) → AUTH → AUTH_OK → TypeConfig → effective MTU.
6. `VpnService.Builder` with real TypeConfig (host IP, DNS, effective MTU) → `establish()` → `detachFd()`.
7. **AttachTun** (native owns FD) → TX/RX dataplane → Connected.

Disconnect / cancel uses `desiredConnected` + op-generation so a late Begin cannot commit Connected.

## Lifetime ownership

VPN lifetime belongs to `NyxveilVpnService` (foreground). Activities only bind/control.

TUN FD: Kotlin `detachFd()` then Go closes on Disconnect. Do not double-close.

## Immutable Control Plane

Base URL is `BuildConfig.CONTROL_PLANE_BASE_URL` (`https://cp.nyxveil.ru:18443`). It is not user-editable and must not be rendered.

While the VPN is up, Control Plane OkHttp sockets go through `VpnService.protect`.

## Bridge

`scripts/build-native.ps1` → `bridge/native/nyxveilbridge.aar` (arm64-v8a `libgojni.so`).
`NvpBridge.nativeAvailable()` is true only after the native library loads and `NativeReady()` succeeds.
