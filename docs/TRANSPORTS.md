# Transports — NVP/1

This document matches the production transport packages under `core/transport/`.

## Implemented Profiles

| Profile | Package | Wire | Role |
|---------|---------|------|------|
| `quic-udp-443` | `transport/quic` | QUIC/UDP + real HTTP/3 CONNECT | Primary |
| `tls-tcp-443` | `transport/tlsstream` | TLS 1.3 / TCP | Fallback |

Selection uses Happy-Eyeballs-style racing via `transport.Registry.DialWithRacing` (`transport.DefaultRacingConfig`: primary QUIC, fallback TLS after 250ms).

## ALPN

### TLS/TCP

- **No application ALPN.** `NextProtos` is left empty on dial and listen.
- NVP version negotiation stays inside the encrypted session handshake.
- Never advertise custom markers (`nvp/1`, product names, generic `vpn`) or fake `h2`.

### QUIC/UDP

- Real **HTTP/3** via `github.com/quic-go/quic-go/http3` with ALPN **`h3` only**.
- Client dials QUIC with `NextProtos: ["h3"]`, sends HTTP `CONNECT` to the node authority, then carries length-prefixed NVP frames on the CONNECT stream.
- Server is an `http3.Server` that accepts `CONNECT` and upgrades to the same `transport.Conn` interface.
- **Must not claim `h3` without speaking HTTP/3.** DATAGRAM path uses HTTP/3 datagrams (RFC 9297) when negotiated.
- There is **no** `nvp/1` (or other custom) ALPN on QUIC.

## Framing

Both HTTP/3 CONNECT stream and TLS/TCP wrap session bytes as:

`length(uint32 BE) || payload`

Length is the payload size; max is `protocol.MaxFrameSize`.

## QUIC DATA Path

- CONTROL uses the reliable CONNECT stream
- DATA prefers **HTTP/3 DATAGRAM** when enabled / peer support is present (`DatagramConn`)
- If datagrams are unavailable, DATA falls back to the stream write path

## TLS/TCP Fallback

- TLS 1.3 minimum (`MinVersion: tls.VersionTLS13`)
- No application ALPN
- Used when QUIC dial fails or loses the race

## MASQUE

- Package: `transport/masque`
- Extension stub only: `masque.Available() == false`
- **Disabled / not registered** in production registries
- Planned profile name reserved as `masque-connect-udp` — not part of NVP/1 shipping transports

## ECH

Encrypted ClientHello policy lives in `transport/ech`. Dial paths apply `DialConfig.ECHPolicy` / `ECHConfigList`. Server helpers: `ech.ApplyServerKeys`, `ech.KeySet` for rotation. See [ECH-DEPLOYMENT.md](ECH-DEPLOYMENT.md).

## Memory Transport

`transport/memory` is **test-only** (`net.Pipe`). Do not use in production.

Integration tests under `core/integration` also exercise **real** TLS and QUIC loopback sockets (`TestRealTLS*`, `TestRealQUIC*`).

## Identity

- Certificate validation via system / provided Root CAs
- Optional SPKI pin (`DialConfig.PinnedPubKey`) verified after handshake (`transport.VerifySPKIPin`)

Core readiness: [CORE-READINESS.md](CORE-READINESS.md). Independent security/cryptographic audit: **NOT PERFORMED**