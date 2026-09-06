# Fingerprint Review

## Methodology

Review of first-connection externally visible characteristics of the NVP/1 reference implementation.

**NOT VERIFIED AGAINST TARGET NETWORK** (no operator DPI equipment available).
**This document does not claim DPI bypass or undetectability.**

## Classification Legend

| Class | Meaning |
|-------|---------|
| **VISIBLE** | Externally observable on the wire or via metadata |
| **PROTECTED** | Inside authenticated/encrypted channel or deliberately avoided |
| **REMOVED** | Previously considered / eliminated from production paths |
| **UNAVOIDABLE** | Inherent to using TLS/QUIC to a VPN node IP |

## Summary Table

| Characteristic | Class | Notes |
|----------------|-------|-------|
| Destination node IP / geolocation | UNAVOIDABLE | Always visible to network observer |
| UDP/TCP port 443 | VISIBLE | Ordinary HTTPS/QUIC ports |
| QUIC or TLS 1.3 handshake | VISIBLE | Real stacks; not browser impersonation |
| ALPN QUIC = `h3` (HTTP/3 CONNECT) | VISIBLE | Genuine HTTP/3; no custom ALPN |
| ALPN TLS = empty `NextProtos` | PROTECTED | No application ALPN; no `nvp/1` |
| Pre-auth NVP magic / product name | REMOVED | No plaintext `NVP1` / `NYX` before secure channel |
| Fake `h2` / fake Chrome impersonation | REMOVED | Not implemented by design |
| Length-prefix framing (4-byte BE) | VISIBLE | After transport crypto; generic pattern |
| Initial handshake application sizes | PROTECTED (padded) | Production padding varies sizes; see handshake padding tests |
| Certificate / TLS fingerprint | UNAVOIDABLE | Unless shared CDN/fronting |
| Timing / volume patterns | UNAVOIDABLE | Traffic analysis not defeated |
| ECH | PROTECTED when negotiated | Policy in `transport/ech`; default deployment NOT VERIFIED |
| Custom ALPN `nvp/1` | REMOVED | TLS has no application ALPN; QUIC uses real `h3` |

## First Application Bytes (after transport)

Inside the TLS/QUIC-protected channel:

- Handshake init/response: padded under production policy (sizes must not be constant)
- No fixed magic string
- No marketing identifiers

## Recommendations for Production

- Deploy behind CDN or shared TLS infrastructure where appropriate
- Enable ECH where infrastructure supports it (Go **1.24+**)
- Rotate node IPs via signed catalog updates
- Variable padding policy for size obfuscation
- Independent fingerprint audit against target networks before commercial claims

Core readiness: [CORE-READINESS.md](CORE-READINESS.md). Independent security/cryptographic audit: **NOT PERFORMED**