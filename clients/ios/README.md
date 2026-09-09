# Nyxveil iOS connection prototype

GitHub workflow `iOS prototype (unsigned)` builds on macOS and uploads a clearly marked **UNSIGNED, NOT INSTALLABLE** IPA plus SHA256/source provenance. Free Apple Personal Team cannot provision Network Extensions; a successful unsigned build does not unlock iPhone VPN testing. See [Apple capability eligibility](https://developer.apple.com/help/account/reference/supported-capabilities-ios/).

Open `Nyxveil.xcodeproj`, scheme **Nyxveil** (iOS 16+). All four targets already exist.

On the Mac, install Go 1.26+ and Xcode, then put your identifiers in `Config/Local.xcconfig`:

```xcconfig
DEVELOPMENT_TEAM = your_actual_team
APP_BUNDLE_IDENTIFIER = your.registered.bundle
TUNNEL_BUNDLE_IDENTIFIER = your.registered.bundle.tunnel
APP_GROUP_IDENTIFIER = group.your.registered.bundle
```

Enable Packet Tunnel, App Group and shared Keychain for both signed app IDs. `PRODUCT_BUNDLE_IDENTIFIER` is derived separately for each target. The shared keychain uses `AppIdentifierPrefix`, including for accounts whose prefix differs from Team ID.

The scheme's pre-build action generates `Frameworks/Nvp.xcframework` using pinned Go module dependencies. If Xcode checks the missing framework before running the pre-action, run `bash scripts/build-core.sh` once, then build again. Keep the repository layout: the binding imports `../windows/third_party/nvp` without modifying it. [Go mobile binding reference](https://go.dev/wiki/Mobile).

On iPhone: enter license → Activate / Load locations → select Location → Connect. iOS asks to add the VPN configuration. The extension obtains fresh catalog and location-scoped ticket independently, including while the app is suspended. Secrets stay in Keychain/process memory; `providerConfiguration` contains only `location_id`. Telemetry uses provider messages. CP is `https://cp.nyxveil.ru:18443`, with system HTTPS trust and redirects disabled.

NVP/1 uses Frozen Core for handshake, PoP AUTH, encryption, replay protection, padding and rekey, through a small Objective-C/Swift binding. The iOS-owned QUIC adapter copies the Frozen HTTP/3 transport and adds cancellation during CONNECT response. No TCP replacement. MTU comes from TypeConfig and the measured QUIC datagram budget. IPv4 default route and server DNS are installed; unsupported IPv6 DATA is dropped. `includeAllNetworks` requests OS traffic capture; actual IPv6 leak behavior, routing across Wi-Fi/LTE and extension memory must be verified on iPhone. [Apple routing reference](https://developer.apple.com/documentation/networkextension/routing-your-vpn-network-traffic).

Refresh endpoint is used for a near-expiry ticket before AUTH. There is no new mid-session ticket protocol: each reconnect issues a fresh location-scoped ticket, allowing CP to select another Node. Retry limit: five failures, 1/2/4/8-second backoff; initial extension startup deadline: 55 seconds. Manual disconnect cancels retries. Connection settings stay installed during reconnect.

Checks run on Windows: `go test -race ./... -count=1 -timeout=40s`, `go vet ./...`, Objective-C binding generation, and project-reference validation. Loopback interop covers actual QUIC/HTTP3 + Frozen Core AUTH + TypeConfig + encrypted DATA roundtrip, catalog tampering, MTU and close. This is **not** iPhone or production E2E. Swift XCTest JSON/TypeConfig tests are included, **not executed** here.

Next gate on macOS: `xcodebuild -project Nyxveil.xcodeproj -scheme Nyxveil -sdk iphoneos CODE_SIGNING_ALLOWED=NO build`, then signed iPhone connection, DNS/VPN IP, Wi-Fi↔LTE, reconnect and manual disconnect. **XCODE BUILD / REAL IPHONE: NOT EXECUTED.** No server/CP blocker was proven; no release or push performed.
