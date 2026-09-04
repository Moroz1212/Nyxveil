# Threat Model

## Scope

NVP/1 VPN Protocol Core protecting user IP traffic between client applications and VPN nodes, with centralized Control Plane for licensing and node catalog.

## Adversary Capabilities Considered

### Network Observer (Passive)
- Packet capture on local network, ISP path, datacenter
- Traffic volume, timing, direction analysis
- DNS query observation (if plaintext DNS used)
- TLS/QUIC metadata (SNI without ECH, IP addresses, port 443)

### Network Attacker (Active)
- MITM on transport handshake
- Packet modification, injection, replay
- Downgrade attacks (TLS version, algorithm)
- UDP blocking, TCP-only forcing
- IP/port filtering
- DNS poisoning of bootstrap endpoints

### Credential Attacker
- Stolen license token (from client app storage)
- Stolen access ticket (short-lived)
- Stolen device (with local keys)
- Authentication brute force

### Infrastructure Attacker
- Compromised VPN node
- Compromised old session key
- Compromised Control Plane (partial — signing key)

### Implementation Attacker
- Malformed packet / parser attacks
- Resource exhaustion (connection flood, oversized frames)

### Operational
- Node outage, datacenter outage
- NAT rebinding, network change (Wi‑Fi → LTE)
- Packet loss, duplication, reorder

## Protections Implemented

| Threat | Mitigation |
|--------|-----------|
| Passive content capture | TLS 1.3 + ChaCha20-Poly1305 session AEAD |
| MITM | Certificate verification, TLS 1.3 minimum, node identity in signed descriptor |
| Replay | Epoch + sequence sliding window |
| Downgrade | TLS 1.3 enforced, reject older versions |
| Stolen long-term password on nodes | Short-lived signed tickets; license never on nodes |
| Malformed input | Strict parsing, max frame size, no panic on hostile input |
| Session hijacking | AEAD + replay window + ephemeral keys |
| Pre-auth fingerprint | No magic bytes; standards-compliant transport |

## What We CANNOT Guarantee

- **Undetectability** — VPN server IP, traffic patterns, and TLS/QUIC metadata may be observable
- **Unblockability** — Resistance to blocking by every network/TSPU: **NOT GUARANTEED**
- **Perfect forward secrecy against all compromises** — compromised endpoint defeats confidentiality for that session
- **Traffic analysis immunity** — encrypted traffic still analyzable by volume/timing/IP
- **DNS privacy without platform integration** — client must use DoH/DoT/system private DNS
- **Instant revocation** — bounded by ticket TTL without active denylist push
- **100% security** — no system is provably unbreakable

## Compromised Node Model

See [NODE-ARCHITECTURE.md](NODE-ARCHITECTURE.md). Node compromise does NOT expose:
- Control Plane private signing key
- Raw license tokens
- Other nodes' secrets

Independent security/cryptographic audit: **NOT PERFORMED**
