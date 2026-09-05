# Traffic Analysis

## Content Confidentiality vs Traffic Analysis Resistance

These are **distinct** properties.

### Content Confidentiality (Implemented)

User IP packet payloads are encrypted:
1. TLS 1.3 protects transport channel
2. NVP session AEAD encrypts inner frames
3. AEAD provides integrity — modification detected

Encryption makes **payload content** unreadable to passive observers.

### Traffic Analysis (Limited Protection)

Even fully encrypted traffic reveals:

| Signal | Observable |
|--------|-----------|
| Server IP | Yes — always visible |
| Port (443) | Yes |
| Packet sizes | Yes — padding can reduce precision |
| Timing patterns | Yes — burst/idle cycles |
| Flow direction | Yes — upload/download volume |
| Connection duration | Yes |
| Number of concurrent flows | Partially |

**Encryption does NOT make traffic analysis impossible.**

## Mitigations (Partial)

- Optional authenticated padding (configurable, variable length)
- ECH (Encrypted ClientHello) — hides SNI from passive observer where supported
- Standards-compliant QUIC/TLS — blends with normal HTTPS traffic class
- No custom pre-auth magic bytes — avoids application-specific fingerprint

## ECH Limitations

- Server IP remains visible
- ECH does not hide traffic volume or timing
- `ECH required` policy prevents silent fallback to plaintext ClientHello
- ECH availability depends on infrastructure — **NOT VERIFIED AGAINST TARGET NETWORK**

## Recommendations for Platform Layer

- Use DoH/DoT for bootstrap DNS (not in crypto core)
- Avoid plaintext DNS for Control Plane hostnames
- Split tunneling handled at OS/TUN layer, not wire protocol

Independent security/cryptographic audit: **NOT PERFORMED**
