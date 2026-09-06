# Operations Guide — NVP/1

## Monitoring

| Component | Endpoint / Signal |
|-----------|-------------------|
| Control Plane | `GET /api/v1/version` |
| Node health | `POST /api/v1/nodes/{id}/health` |
| Revocation freshness | `updated_at` in `/api/v1/revocation` |
| Session metrics | `internal/metrics` (wire on node) |

## Key Rotation

### Ticket signing keys (Control Plane)
1. Generate new Ed25519 keypair
2. Add as `NextKeyID` in issuer config (dual-key period)
3. Deploy CP with both keys active
4. Update nodes with both public keys in `VerifierConfig.PublicKeys`
5. After ticket TTL elapsed, remove old key

### Catalog signing keys
Same procedure using `catalog.VerifyKeys`.

### Node identity keys
1. Register new public key in `node_identities`
2. Rolling restart nodes with new private key
3. Disable old identity after all nodes migrated

## Rate Limiting

Control Plane defaults: 120 req/min per IP, burst 30. Tune via `ServerOptions.RateLimit`.

## Backup

- SQLite: snapshot `cp.db` daily
- Keys directory: encrypted backup of `issuer.key` and `catalog.key`
- `NVP_LICENSE_KEK`: back up with the same protection as signing keys (64 hex chars)

See [INCIDENT-RESPONSE.md](INCIDENT-RESPONSE.md) for compromise procedures.
