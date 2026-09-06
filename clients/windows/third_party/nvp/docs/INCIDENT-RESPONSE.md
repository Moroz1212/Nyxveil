# Incident Response — NVP/1

## Key Compromise — Control Plane Signing Key

1. **Immediate**: Disable compromised key ID in issuer config
2. Issue emergency revocation for all active JTIs via `POST /device/remove` and license revoke
3. Rotate to new keypair in `-keys` directory
4. Force client reconnect (tickets expire within TTL, max 15 min default)
5. Audit access logs for anomalous ticket issuance

## Key Compromise — VPN Node

1. Drain node: `POST /api/v1/nodes/{id}/drain`
2. Revoke node identity in `node_identities`
3. Rotate node Ed25519 key and redeploy
4. Blast radius: sessions on compromised node only; no CP signing key exposure

## License / Device Compromise

1. `POST /api/v1/device/remove` → propagates to revocation list
2. Nodes sync within 60s via `SyncCache`
3. For urgent block: reduce ticket TTL before incident

## Data Breach — SQLite DB

1. Rotate all license tokens (re-issue licenses)
2. Invalidate all device registrations
3. Force re-activation on next client connect

## Эскалация

Повторный внутренний разбор кода по [SECURITY-AUDIT-PREP.md](SECURITY-AUDIT-PREP.md) и [SECURITY-AUDIT-REPORT.md](SECURITY-AUDIT-REPORT.md). Внешняя лаборатория не привлекается.
