# Control Plane API Contract

Base URL: `https://control.nyxveil.example/api/v1`

All endpoints require HTTPS. Authentication via license token or node service identity as specified.

## Endpoints

### License

| Method | Path | Description |
|--------|------|-------------|
| POST | `/license/validate` | Validate license token |

### Device

| Method | Path | Description |
|--------|------|-------------|
| POST | `/device/activate` | Register device public key |
| POST | `/device/remove` | Remove device from license |

### Access Ticket

| Method | Path | Description |
|--------|------|-------------|
| POST | `/ticket/issue` | Issue short-lived VPN access ticket |
| POST | `/ticket/refresh` | Refresh existing ticket |

### Catalog

| Method | Path | Description |
|--------|------|-------------|
| GET | `/catalog` | Signed node/location catalog |
| GET | `/locations` | Location list |
| GET | `/nodes` | Node registry (admin) |

### Node Operations

| Method | Path | Description |
|--------|------|-------------|
| POST | `/nodes/{node_id}/health` | Node heartbeat |
| POST | `/nodes/{node_id}/drain` | Enable draining mode |
| POST | `/nodes/{node_id}/maintenance` | Maintenance mode |

### Other

| Method | Path | Description |
|--------|------|-------------|
| GET | `/revocation` | Revocation list (JTIs, licenses, devices) |
| GET | `/version` | Protocol version info |
| POST | `/master/access` | Master role access (normal auth flow) |

## Node Authentication

Nodes authenticate to Control Plane with unique service identity (mTLS or Ed25519-signed requests). User tokens are NOT used for node heartbeat.

## Catalog Security

Catalog MUST be signed or delivered over authenticated HTTPS. Client verifies signature before trusting node public keys.

## test_only Filtering

Control Plane filters test nodes from user catalog responses. Master role receives test + production nodes.

See `controlplane/api/contract.go` for Go type definitions.
