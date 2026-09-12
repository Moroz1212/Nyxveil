# Architecture

Nyxveil **server** is a production VPN **node** process. It is not the Control Plane and not a client.

## Components

```
                    ┌─────────────────────┐
   clients ────────►│ TLS :443 / QUIC :443│
                    └──────────┬──────────┘
                               │ session / ticket auth
                    ┌──────────▼──────────┐
                    │   sessions.Manager  │
                    └──────────┬──────────┘
                               │
                    ┌──────────▼──────────┐
                    │  TUN nyxveil0 + NAT │──► internet
                    └─────────────────────┘
                               ▲
                    ┌──────────┴──────────┐
                    │ Control Plane HTTPS │  heartbeat, config, revocation
                    └─────────────────────┘
```

| Package | Role |
|---------|------|
| `cmd/nyxveil-server` | Process entry; `--register` bootstrap |
| `cmd/nyxveilctl` | Operator CLI via unix control socket / systemd |
| `internal/runtime` | Lifecycle: CP client, listeners, TUN, heartbeat |
| `internal/identity` | Ed25519 `node.key` (never leaves the host) |
| `internal/localconfig` | `/etc/nyxveil/server.json` |
| `internal/controlplane` | Register / health / config / revocation APIs |
| `internal/nodeauth` | `nvp-node-req-v2` request signing |
| `third_party/nvp` | Frozen Protocol Core (wire, crypto, transports) |

## Trust boundaries

1. **Bootstrap token** — one-shot Control Plane registration; not persisted.
2. **Node private key** — `/var/lib/nyxveil/node.key` (0600, user `nyxveil`).
3. **Client tickets** — verified with Control Plane issuer keys (protocol core).
4. **Host firewall** — isolated `inet nyxveil` nftables table only.

## Runtime identity

On first successful install the node:

1. Writes non-secret `server.json` (includes chosen `node_id`).
2. Creates or reuses `node.key`.
3. Calls Control Plane `POST /api/v1/nodes/register`.
4. Starts systemd unit `nyxveil-server`.

Repair reinstalls preserve `node.key` and `node_id`.
