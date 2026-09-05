# ECH Deployment — NVP/1

## Overview

Encrypted Client Hello (ECH) hides SNI from network observers. NVP/1 supports ECH via `transport/ech` with policies:
- `preferred` — use ECH when DNS HTTPS record provides config
- `required` — fail if ECH not negotiated

## Server Setup

1. Generate ECH keypair (X25519) using your TLS stack or `openssl ech`
2. Publish `HTTPS` DNS record with `echconfig` for your domain
3. Configure TLS listener with ECH keys (Go 1.24+ `crypto/tls` EncryptedClientHelloKeys)

## Client Setup

Provide ECH config list via Connector / DialConfigProvider (wired into every DialConfig):

```go
connector.ECHPolicy = transport.ECHRequired
connector.ECHConfigList = echConfigListFromDNS
// or DialConfigProvider.ECHPolicy() / ECHConfigList()
```

`ECHRequired` with an empty config list fails **before dial**.

Server listeners:

```go
ech.ApplyServerKeys(tlsConfig, keys)
// rotation:
set := ech.NewKeySet(keys)
set.Rotate(newKeys)
set.ApplyTo(tlsConfig)
```

After handshake, verify:

```go
ech.VerifyNegotiated(policy, conn.ConnectionState())
```

## Diagnostics

```bash
go run ./core/cmd/nvp-diag/ -host vpn.example.com -port 443
```

Look for `ech_status: OK (accepted)` or `NOT_ACCEPTED`.

## Key Rotation

1. Publish new ECH config in DNS with dual configs during overlap
2. Clients refresh DNS TTL before removing old config
3. Monitor `ECHAccepted` metric on nodes

Real-network ECH verification: **NOT VERIFIED AGAINST TARGET NETWORK**

Independent security/cryptographic audit: **NOT PERFORMED**
