# Android security notes

- **Cleartext disabled** (`usesCleartextTraffic=false` + `network_security_config` system trust only).
- **No** Contacts / SMS / Location permissions.
- License blob and device Ed25519 identity live in **EncryptedSharedPreferences** (Android Keystore-backed MasterKey).
- License token is **never logged**.
- Catalog: reject bad signatures and expired catalogs; valid cache only as fallback when fresh fetch fails.
- Diagnostics redacts secrets and omits Control Plane URL.
- VPN `protect(fd)` is exposed for the engine so CP/ticket sockets bypass the tunnel.
- `onRevoke` tears down TUN and clears desired-connected so reconnect loops stop.
- Debug `applicationId` uses `.debug` suffix; release minify enabled in Gradle.
