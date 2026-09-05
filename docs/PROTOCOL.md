# Nyxveil Protocol (NVP/1)

NVP/1 is the internal wire protocol for Nyxveil commercial VPN platform.

**Protocol version:** `NVP/1` · **Core version:** `1.0.0` · **Go:** 1.24+  
See [CORE-READINESS.md](CORE-READINESS.md).

## Design Principles

1. **No custom cryptography** — X25519, HKDF-SHA256, ChaCha20-Poly1305, Ed25519, TLS 1.3
2. **Transport abstraction** — session core independent of UDP/TCP/QUIC
3. **No pre-auth magic bytes** — no plaintext protocol identifiers before TLS protection
4. **Standards-compliant transports** — real QUIC/TLS, not fake impersonation
5. **Short-lived access tickets** — license credentials never sent to VPN nodes

## Layer Model

```
┌─────────────────────────────────────┐
│  IP Packets (TUN interface)       │
├─────────────────────────────────────┤
│  NVP Session Layer (AEAD frames)  │
│  CONTROL + DATA separation        │
├─────────────────────────────────────┤
│  Transport Framing (length-prefix)  │
├─────────────────────────────────────┤
│  QUIC/UDP:443  or  TLS/TCP:443      │
└─────────────────────────────────────┘
```

## Session Establishment

1. Transport connect (QUIC preferred, TLS/TCP fallback) within the desired location
2. TLS 1.3 handshake (no NVP plaintext before this); optional ECH (`KeySet` snapshot at listener build)
3. X25519 handshake inside TLS-protected channel
4. AUTH with signed access ticket (JWT Ed25519; default location-scoped, optional `NodeScope`)
5. ESTABLISHED — DATA frames permitted

Automatic failover is **same location only**. Cross-location requires a new application `OpenSession`.
## Protocol Version

Internal version identifier: `NVP/1` (conveyed only inside authenticated channel).

Version numbers: min=1, max=1, current=1.

## Related Documents

- [CRYPTOGRAPHY.md](CRYPTOGRAPHY.md)
- [WIRE-FORMAT.md](WIRE-FORMAT.md)
- [STATE-MACHINE.md](STATE-MACHINE.md)
- [AUTH-ARCHITECTURE.md](AUTH-ARCHITECTURE.md)

Independent security/cryptographic audit: **NOT PERFORMED**
