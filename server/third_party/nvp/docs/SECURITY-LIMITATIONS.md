# Security Limitations

## Explicit Non-Guarantees

1. **Resistance to blocking by every network/TSPU: NOT GUARANTEED**
2. **Protocol undetectability / DPI bypass: NOT GUARANTEED** (and not claimed)
3. **Traffic analysis immunity: NOT GUARANTEED**
4. **100% security: NOT POSSIBLE**
5. **Third-party lab audit: not part of the project process** (internal cyclic reviews only)

## Known Limitations

### Network

- Server IP always visible (UNAVOIDABLE)
- QUIC/TLS on 443 is classifiable
- Length-prefix framing is an observable pattern inside the secure channel
- ALPN: TLS uses **no** application ALPN; QUIC uses real HTTP/3 (`h3` + CONNECT). Custom markers (`nvp/1`) and fake `h2` are forbidden
- No claim of censorship circumvention against state DPI/TSPU
- Automatic Core failover is **same-location only**; cross-location is a new session by the app (not silent Core hopping)

### Authentication

- Revocation delay bounded by ticket TTL plus sync interval
- `max_devices` enforced at Control Plane, not per packet on the node
- Catalog requires auth; revocation sync requires node identity — misconfiguration can widen exposure
- Ticket refresh never widens location/`NodeScope`; default tickets are location-scoped

### Cryptographic

- Session rekey uses fresh X25519 ECDH per epoch
- ECH via `transport/ech` (DNS HTTPS required for required mode); deployment NOT VERIFIED by default
- Server ECH: `KeySet` applied at listener build; Core 1.0 does not live-rotate mid-connection via `GetEncryptedClientHelloKeys` — reconfigure the listener
- MASQUE: interface stub only (`Available() == false`); disabled in NVP/1
- AUTH binds to NVP handshake transcript, not TLS exporter

### Platform

- DNS privacy is a platform concern (DoH/DoT)
- Split tunneling is not in the wire protocol
- Windows/Android TUN drivers are foundations, not full apps

### Operational

- SQLite is single-instance; set `NVP_LICENSE_KEK` (64 hex chars) in production
- No billing in protocol core
- Heartbeats must use `UpdateNodeHealth` so SPKI/config is not overwritten

## Commercial launch

Not a production VPN service until DPI/TSPU is tested on target networks and client apps are complete. Internal code audits do not replace that.

Independent security/cryptographic audit: **NOT PERFORMED**