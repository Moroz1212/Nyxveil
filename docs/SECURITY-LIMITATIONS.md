# Security Limitations

## Explicit Non-Guarantees

1. **Resistance to blocking by every network/TSPU: NOT GUARANTEED**
2. **Protocol undetectability: NOT GUARANTEED**
3. **Traffic analysis immunity: NOT GUARANTEED**
4. **100% security: NOT POSSIBLE**
5. **Independent security audit: NOT PERFORMED**

## Known Limitations

### Cryptographic
- Session layer rekey uses fresh X25519 ECDH exchange per epoch
- ECH supported via `transport/ech` policy (requires DNS HTTPS config for required mode)
- MASQUE transport: interface stub only (`transport/masque`)

### Network
- Server IP always visible to observer
- QUIC/TLS on port 443 is classifiable traffic
- Length-prefix framing is observable pattern (generic)

### Authentication
- Revocation delay bounded by ticket TTL without active denylist sync
- max_devices enforced at Control Plane, not re-checked per packet on node

### Platform
- DNS privacy requires platform-layer DoH/DoT integration
- Split tunneling not in wire protocol — platform responsibility
- Windows/Android TUN drivers not in this core

### Operational
- No payment/billing in protocol core
- Control Plane reference API only — production deployment requires hardening
- Benchmarks environment-dependent

## Commercial Readiness Gate

Minimum requirements for production claim:
- [ ] Independent cryptographic audit
- [ ] Production Control Plane deployment
- [ ] Real-network DPI testing documented
- [ ] ECH infrastructure deployed
- [ ] Revocation push mechanism operational
- [ ] mTLS node ↔ Control Plane in production

Current reference implementation status: **development foundation**

Independent security/cryptographic audit: **NOT PERFORMED**
