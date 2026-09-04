# Security Audit Preparation

This document prepares Nyxveil Protocol Core (NVP/1) for independent security/cryptographic audit.

**Status: Independent security/cryptographic audit NOT PERFORMED**

## Scope for Auditors

### In Scope
- NVP/1 session layer (X25519, HKDF-SHA256, ChaCha20-Poly1305)
- Access ticket JWT validation (Ed25519, strict alg allowlist)
- Replay protection (epoch + sliding window)
- State machine enforcement
- Transport TLS 1.3 / QUIC integration
- Control Plane ticket issuance and catalog signing
- Pre-authentication fingerprint properties

### Out of Scope (v1)
- Payment/billing systems
- Production deployment hardening (WAF, HSM)
- Windows/Android GUI applications
- MASQUE transport (stub only)
- Real-network DPI validation

## Cryptographic Inventory

| Component | Algorithm | Library |
|-----------|-----------|---------|
| Key agreement | X25519 | golang.org/x/crypto |
| KDF | HKDF-SHA256 | golang.org/x/crypto |
| AEAD | ChaCha20-Poly1305 | golang.org/x/crypto |
| Tickets | Ed25519 (JWT) | crypto/ed25519, golang-jwt |
| Transport | TLS 1.3 | crypto/tls |
| Random | CSPRNG | crypto/rand |

No custom ciphers, MACs, curves, or KDFs.

## Critical Review Areas

1. **Nonce reuse** — epoch+sequence nonce construction per direction
2. **Rekey transition** — fresh ECDH rekey with epoch overlap window
3. **Ticket validation** — alg allowlist, aud/iss/exp/nbf, device binding
4. **Replay window** — reorder tolerance vs duplicate rejection
5. **State machine** — DATA before AUTH, rekey before establishment
6. **Parser safety** — max frame sizes, no panic on hostile input
7. **ECH policy** — required mode must not silently fallback
8. **Node compromise** — blast radius documentation

## Test Evidence for Auditors

```bash
go test ./...
go test -race ./integration/... ./replay/... ./auth/ticket/...
go test -fuzz=FuzzDecodeWireRecord -fuzztime=30s ./packet/
go test -fuzz=FuzzTicketVerify -fuzztime=30s ./auth/ticket/
```

Security negative tests in `integration/`:
- Tampered ciphertext, wrong CA, wrong SNI
- Expired/wrong device/revoked tickets
- Replay window rejection
- DATA-before-AUTH rejection

## Known Limitations (Pre-Audit)

See [SECURITY-LIMITATIONS.md](SECURITY-LIMITATIONS.md).

## Audit Deliverables Requested

1. Cryptographic protocol review (NVP/1 wire format)
2. Implementation review (Go codebase)
3. Control Plane auth architecture review
4. Fingerprint / traffic analysis assessment (no false bypass claims)
5. Remediation priority list

Independent security/cryptographic audit: **NOT PERFORMED**
