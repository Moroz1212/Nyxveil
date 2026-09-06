# Deployment Guide — NVP/1

## Control Plane (Production)

### Requirements
- Go 1.24+
- TLS certificate (Let's Encrypt or internal CA)
- Persistent volume for SQLite (`-db`) and keys (`-keys`)

### Run

```bash
export NVP_LICENSE_KEK=$(openssl rand -hex 32)   # 32-byte key, 64 hex chars
go build -o nvp-controlplane ./core/cmd/nvp-controlplane/
./nvp-controlplane \
  -listen :8443 \
  -production \
  -db /var/lib/nvp/cp.db \
  -keys /var/lib/nvp/keys \
  -cert /etc/nvp/tls.crt \
  -key /etc/nvp/tls.key \
  -issuer https://control.example.com
```

License token format: `license_id:secret` (demo: `nyx_lic_demo:change-me`). With `NVP_LICENSE_KEK` set, secrets are ChaCha20-Poly1305-encrypted in SQLite.

### Node Registration

1. Generate node Ed25519 keypair
2. Register public key via `RegisterNodeIdentity`
3. Node heartbeats use signed `node_token` (see `auth/nodeauth`)

### Revocation Sync

VPN nodes poll `GET /api/v1/revocation` via `node/revocation.SyncCache` (default 60s interval).

## VPN Node

Deploy `nvp-test-server` as reference; production nodes require:
- TLS 1.3 / QUIC listener on :443
- `authhandler.AuthHandler` with `VerifierConfig.Revoked` wired to `SyncCache`
- Catalog signature verification keys from Control Plane

## ECH

See [ECH-DEPLOYMENT.md](ECH-DEPLOYMENT.md).

## HA Note

SQLite is single-instance. For HA, migrate `server/controlplane/store` to PostgreSQL (schema-compatible) behind load balancer with shared DB.

Internal security reviews: [SECURITY-AUDIT-REPORT.md](SECURITY-AUDIT-REPORT.md). External lab audit is not part of the process.
