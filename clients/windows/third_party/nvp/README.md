# Nyxveil Protocol Core (NVP/1)

Go implementation of the **Nyxveil Protocol (NVP/1)** commercial VPN control and data-plane core (**Core 1.0.0**).

This repository ships the **protocol core** (`core/`), documentation (`docs/`), and scaffolding for server/client products. It is **not** a claim of undetectable or censorship-proof traffic. See `docs/THREAT-MODEL.md`, `docs/TRAFFIC-ANALYSIS.md`, `docs/SECURITY-LIMITATIONS.md`, and **`docs/CORE-READINESS.md`**.

**Requires Go 1.24+** (ECH / `EncryptedClientHelloKeys`)

## Transports (accurate)

| Profile | Wire | ALPN |
|---------|------|------|
| Primary `quic-udp-443` | QUIC + **real HTTP/3 CONNECT** | **`h3` only** (no `nvp/1` ALPN) |
| Fallback `tls-tcp-443` | TLS 1.3 / TCP | **No application ALPN** (empty `NextProtos`) |
| MASQUE | Stub only | **Disabled** — not registered in NVP/1 |

## Commercial flow (honest summary)

1. **Control Plane** issues licenses, device activation, catalogs, and short-lived session tickets.
2. **Client** validates license/device with Control Plane, obtains a **location-scoped** ticket (default; optional restrictive `NodeScope`) and signed node catalog (catalog endpoints require auth). Ticket **refresh never widens** scope and rebuilds role/permissions/locations from **current** entitlements (preserves `NodeScope` when unrestricted).
3. **Client** connects to a **Node** over TLS stream / QUIC HTTP/3 CONNECT (optional ECH policy; server `KeySet` applied at listener build — rotate by reconfiguring the listener).
4. **Node** verifies the ticket; client **AUTH waits for ESTABLISHED** (`AUTH_OK`) before success.
5. **Data plane** carries padded/authenticated inner frames; **same-location** multi-node + transport failover (`failover.ExhaustedError` lists tried node IDs, no secrets). Cross-location requires a new `OpenSession` by the app.
6. Revocation and capacity are enforced via Control Plane / node policy — not by obscurity.

## Layout

```
Nyxveil/
├── core/                 # Protocol core (library + cmd tools)
├── server/               # Product server scaffolding
├── client/               # Product client scaffolding
└── docs/                 # Protocol, crypto, wire format, threat model, …
```

## Build & Test

```bash
gofmt -w ./core
go vet ./...
go build ./...
go test ./...
go test -race ./core/...
go test -count=10 ./core/...
go test -shuffle=on -count=5 ./core/...
go test ./core/integration/ -count=1   # includes real TLS/QUIC loopback
```

## Documentation

Start with `docs/CORE-READINESS.md`, then `docs/PROTOCOL.md`, `docs/TRANSPORTS.md`, `docs/AUTH-ARCHITECTURE.md`, `docs/FAILOVER.md`, and `docs/SECURITY-LIMITATIONS.md`. Benchmarks: `docs/BENCHMARKS.md`.

## Status

**Protocol Core freeze:** buildable, tested libraries under `core/` ready for server/client integration. Deferred: ISP/TSPU stands, Wintun/Android product paths, independent external audit. Full consumer apps under `server/` / `client/` remain scaffolding.
