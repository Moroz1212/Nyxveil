# Core Readiness — NVP/1 Protocol Core 1.0.0

**Protocol version:** `NVP/1`  
**Core version:** `1.0.0`  
**Go toolchain:** **1.24+** (required for ECH / `crypto/tls` EncryptedClientHelloKeys)  
**Status:** Protocol Core is **ready for server/client product integration**.

This document is the **source of truth** for core readiness. Older commercial/security reports under [`docs/history/`](history/) are historical and may say “NOT READY” for a full consumer VPN product — that is about product surfaces (DPI stands, apps, audit), not about freezing the protocol core libraries.

---

## Ready (in scope)

| Area | Notes |
|------|--------|
| Session / AEAD / rekey / replay | `core/session`, packet codec, ECDH rekey + overlap |
| Auth tickets | Location-scoped JWT; device binding; node `AuthHandler` |
| AUTH → ESTABLISHED | Connector `OpenSession` waits for `AUTH_OK` / `StateEstablished` |
| Transports | TLS 1.3 **no application ALPN**; QUIC **real HTTP/3 CONNECT** (`h3` only) |
| Failover | Same-location node + transport racing only; typed `failover.ExhaustedError`; cross-location = new `OpenSession` by app |
| Control Plane contract | License, device, ticket issue/refresh, signed catalog, revocation |
| Ticket refresh | Rebuilds from **current** entitlements; never widens; preserves `NodeScope` when unrestricted |
| Tests | Unit, integration (incl. **real socket** TLS/QUIC loopback), race (CI), fuzz smoke |

## Deferred (out of core freeze)

| Item | Reason |
|------|--------|
| ISP / TSPU / operator DPI stands | Not available; no bypass claims |
| Wintun / full Windows TUN product path | Platform foundation only |
| Android VpnService product app | Scaffolding / foundation only |
| Independent external security audit | **NOT PERFORMED** |
| MASQUE | Stub (`Available()==false`); not registered in NVP/1 |
| Full consumer VPN UX under `server/` / `client/` | Scaffolding |

## Non-guarantees

- Undetectability / censorship resistance: **not guaranteed**
- Traffic-analysis immunity: **not guaranteed**
- “100% security”: **impossible**

## Integration checklist (server / client)

1. Require **Go 1.24+**
2. Register only `quic-udp-443` + `tls-tcp-443` transports
3. Issue **location-scoped** tickets by default (`location_id` set, `node_id` empty); optional restrictive `NodeScope` via `node_id`
4. Call `OpenSession` and treat success only when session is **ESTABLISHED**
5. Start `ReadLoop` after `OpenSession` (temp auth read is stopped cleanly)
6. On failover exhaustion, handle `*failover.ExhaustedError` / `errors.Is(..., nvperr.ErrTransportUnavailable|ErrNoHealthyNodes)` — tried node IDs only, no secrets. For another location, open a **new** session (Core does not auto cross-location)
7. Honor `protocol.Default*` for rekey / padding / `DefaultAuthTimeout` (15s) unless overridden
8. ECH: apply `ech.KeySet` at listener build (`serverconfig`); rotate by rebuilding/reconfiguring the listener

## Defaults (reference)

| Constant | Value |
|----------|--------|
| `protocol.DefaultAuthTimeout` | 15s |
| `protocol.DefaultRekeyInterval` | 30m |
| `protocol.DefaultRekeyPacketCount` | 1_000_000 |
| `protocol.DefaultRekeyByteCount` | 1 GiB |
| `protocol.DefaultReplayWindow` | 1024 |
| Padding | random-range 0–64 bytes (enabled) |

See also: [PROTOCOL.md](PROTOCOL.md), [TRANSPORTS.md](TRANSPORTS.md), [AUTH-ARCHITECTURE.md](AUTH-ARCHITECTURE.md), [FAILOVER.md](FAILOVER.md), [TESTING.md](TESTING.md).
