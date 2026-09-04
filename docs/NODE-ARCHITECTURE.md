# Node Architecture

## Node Identity

Each VPN node has:
- `node_id` (e.g., `fi-hel-01`)
- Asymmetric service identity for Control Plane auth
- TLS server certificate for client connections
- Ed25519 server identity key (in signed descriptor)

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

Delivered via signed catalog from Control Plane.

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

## Horizontal Scale

Nodes validate access tickets locally via JWT signature. Adding nodes requires:
1. Register in Control Plane
2. Deploy node binary + identity
3. Appear in signed catalog

No per-user secret distribution to nodes.

## Compromised Node Consequences

| Asset | Exposed? |
|-------|----------|
| Active session traffic | Yes (that node only) |
| Past sessions (with FS) | No (if ephemeral keys destroyed) |
| License tokens | No |
| CP signing key | No |
| Other nodes | No |

## Draining Mode

Node stops accepting new sessions, existing sessions complete gracefully. Used for updates and rollout.

## Identity Rotation

1. Generate new server identity key
2. Control Plane publishes updated signed descriptor
3. Clients receive via catalog refresh
4. Old key deprecated after transition period

Independent security/cryptographic audit: **NOT PERFORMED**
