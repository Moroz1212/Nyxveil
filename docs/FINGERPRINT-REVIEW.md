# Fingerprint Review

## Methodology

Review of first-connection externally visible characteristics of NVP/1 reference implementation.

**NOT VERIFIED AGAINST TARGET NETWORK** (no operator DPI equipment available).

## First Connection Observations

| Characteristic | Value |
|---------------|-------|
| Primary transport | QUIC UDP port 443 |
| Fallback transport | TLS 1.3 TCP port 443 |
| ALPN (QUIC) | `h3` |
| ALPN (TCP) | `h2` |
| TLS minimum | 1.3 |
| Pre-auth NVP magic bytes | **None** |
| Pre-auth plaintext protocol name | **None** |
| ECH | Architecture supports; default NOT VERIFIED |

## First Application Bytes (after transport)

Inside TLS-protected channel:
- Handshake init: 34 bytes (version + X25519 pubkey)
- No fixed magic string
- No "NVP1", "NYXVEIL", or marketing identifiers

## UNAVOIDABLE SIGNALS

- Destination IP address of VPN node
- Port 443 usage
- QUIC or TLS protocol presence
- Connection timing and volume patterns
- Certificate/TLS fingerprint of server (unless shared with CDN)
- IP geolocation of server

## IMPLEMENTATION-CREATED SIGNALS (Reviewed)

| Signal | Status |
|--------|--------|
| Custom magic hello bytes | **Eliminated** |
| Fixed pre-auth protocol version string | **Eliminated** |
| Marketing name in wire traffic | **Eliminated** |
| Length-prefix framing (4-byte BE) | Present — generic, used by many protocols |
| Handshake size (34/38 bytes) | Present — minimal; not unique identifier alone |
| Fixed ALPN h3/h2 | Present — standards-compliant; common |
| Fake browser impersonation | **Not implemented** (by design) |

## Fake Impersonation

We deliberately do **not** implement fake Chrome/YouTube impersonation. Transport is genuine QUIC/TLS.

## Recommendations for Production

- Deploy behind CDN or shared TLS infrastructure where appropriate
- Enable ECH where infrastructure supports
- Rotate node IPs via signed catalog updates
- Variable padding policy for size obfuscation
- Independent fingerprint audit against target networks before commercial claims

Independent security/cryptographic audit: **NOT PERFORMED**
