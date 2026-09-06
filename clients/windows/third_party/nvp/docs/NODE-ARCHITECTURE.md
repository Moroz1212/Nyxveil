# Node Architecture

## Node Identity

Each VPN node has:

- `node_id` (e.g., `fi-hel-01`)
- Asymmetric service identity for Control Plane auth
- TLS server certificate for client connections
- Ed25519 server identity key (in signed descriptor)
- Optional **SPKI pin** published in the catalog / node registry for client pin verification

## Node Descriptor

```json
{
  "node_id": "fi-hel-01",
  "location_id": "fi-hel",
  "country": "FI",
  "city": "Helsinki",
  "endpoints": [{"host": "...", "port": 443, "profiles": ["quic-udp-443", "tls-tcp-443"]}],
  "server_identity_key": "...",
  "protocol_version": 1
}
```

Delivered via signed catalog from Control Plane. Production clients may require non-empty `SPKIPin` (`RequirePin`).

## Control Plane Registry: UpsertNode vs UpdateNodeHealth

| API | Store method | Writes |
|-----|--------------|--------|
| Admin / full register | `UpsertNode` → `CreateOrUpdateNodeConfig` | All static fields including endpoints, `server_name`, **`spki_pin`**, protocol/server version, enabled/test_only/draining |
| Heartbeat | `UpdateNodeHealth` | **Only** `health_json`, `current_sessions`, optional `capacity`, `last_seen` |

Heartbeats must never clear or overwrite SPKI pins or static configuration (`TestHeartbeatDoesNotOverwriteNodeConfiguration`, `TestSPKIPinPersistsAcrossRestart`).

## SPKI Persistence

`nodes.spki_pin` is a BLOB column (added via idempotent `ALTER TABLE` migration). Pins survive Control Plane restarts and health updates. Clients verify peer certificate SPKI against the catalog pin after TLS/QUIC handshake.

## Local Persistent State (Allowed)

- Node identity / service credentials
- Node configuration
- TLS server key
- Control Plane verification public keys (current + next)
- Local revocation/deny cache
- Operational metrics

## NOT Stored on Node

- User license tokens
- Full subscription database
- Control Plane signing private key
- Other nodes' secrets

## Heartbeat

Periodic secure heartbeat to Control Plane:

- health, version, capacity, sessions, load, transports
- Persisted via `UpdateNodeHealth` only

## Horizontal Scale

Nodes validate access tickets locally via JWT signature. Adding nodes requires:

1. Register in Control Plane (`CreateOrUpdateNodeConfig` / `UpsertNode`) with SPKI
2. Deploy node binary + identity
3. Appear in signed catalog

No per-user secret distribution to nodes.

Clients fail over across healthy nodes **within one location** when the ticket is location-scoped (`NodeScope` empty). Node-scoped tickets pin `AllowedNodeIDs`. Cross-location selection is an application `OpenSession` decision, not Core automatic failover.

ECH keys for node listeners are applied as a `KeySet` snapshot at listener build time; rotate by reconfiguring/rebuilding the listener (see `transport/serverconfig`).

## Compromised Node Consequences

| Asset | Exposed? |
|-------|----------|
| Active session traffic | Yes (that node only) |
| Past sessions (with FS) | No (if ephemeral keys destroyed) |
| License tokens | No |
| CP signing key | No |
| Other nodes | No |

## Draining Mode

Node stops accepting new sessions; existing sessions complete gracefully. Used for updates and rollout.

## Identity Rotation

1. Generate new server identity key / certificate
2. Control Plane publishes updated signed descriptor + SPKI
3. Clients receive via catalog refresh
4. Old key deprecated after transition period

Independent security/cryptographic audit: **NOT PERFORMED**