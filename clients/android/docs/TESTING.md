# Testing (Android)

## Unit tests

```powershell
.\scripts\test.ps1
# or
.\gradlew.bat :app:testDebugUnitTest :bridge:testDebugUnitTest
```

Covered today:

- `CatalogVerifierTest` — expired catalog fails temporal validation after a valid Ed25519 signature
- `HostIpAndTypeConfigTest` — host IP preserved; TypeConfig → effective MTU mapping
- `NvpBridgeVersionTest` — on JVM without arm64 `.so`, `nativeAvailable()==false` (never hardcoded true)
- Go `bridge/go`: Version/Protocol, MTU (Windows 1135 lesson), netcfg host-IP, Disconnect opGen

## One-command build gates

```powershell
.\scripts\build-apk.ps1
```

Asserts Frozen Core, gomobile AAR (`libgojni.so`), Gradle tests, APK contains `lib/arm64-v8a/libgojni.so`.

## Device smoke (next iteration)

```powershell
.\scripts\build-apk.ps1 -Install
```

Manual LIVE checks when a phone is connected:

- License → Helsinki → AUTH / TypeConfig / VPN IP / TX / RX / DNS
- Disconnect / reconnect; Diagnostics shows `native`
