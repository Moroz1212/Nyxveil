# Control Plane

The node is a **client** of the Nyxveil Control Plane.

## Registration

Bootstrap (installer):

```bash
printf '%s\n' "$BOOTSTRAP_TOKEN" | nyxveil-server --config /etc/nyxveil/server.json --register-stdin
```

(`--register TOKEN` remains for compatibility; prefer stdin so the token is not on argv.)

Sends `POST /api/v1/nodes/register` with:

- `bootstrap_token` (fresh node) or `node_token` PoP (existing key)
- `node_id`, `location_id`, `display_name`
- Ed25519 `public_key` / `public_identity` from `node.key`
- TLS + QUIC `endpoints` from `public_host` and listen ports

Response updates operational awareness (`node_id`, `config_version`). Local `server.json` already holds the chosen `node_id`.

## Ongoing APIs (signed)

After registration, requests use **nvp-node-req-v2** headers (`internal/nodeauth`):

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/nodes/{id}/health` | Heartbeat / load |
| GET | `/api/v1/node/config` | Desired node config |
| GET | `/api/v1/revocation` | Revocation snapshot |
| GET | `/api/v1/node/ticket-keys` | Access Ticket verification public keys |

## Local config fields

`/etc/nyxveil/server.json` (no secrets):

- `control_plane_url`
- `node_id`, `location_id`, `display_name`
- `public_host`, `tls_listen`, `quic_listen`
- `vpn_subnet_cidr` (default `10.66.0.0/24`)
- `heartbeat_seconds`

Applied CP config snapshot: `/var/lib/nyxveil/applied-config.json` (writable by `nyxveil`).

`/etc/nyxveil/server.json` is installer/static bootstrap only — the daemon never writes it.
On startup, applied `location_id` / `config_version` override bootstrap values in memory (AuthHandler + Status).

## Operator checks

```bash
nyxveilctl status   # includes cp_connected
nyxveilctl health
```
