# Control Plane API Contract

Base URL: `https://control.nyxveil.example/api/v1`

All endpoints require HTTPS in production. Authentication via license token, access ticket, or node service identity as specified.

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
| POST | `/ticket/issue` | Issue short-lived VPN access ticket (default location-scoped; optional node pin via `node_id`) |
| POST | `/ticket/refresh` | Refresh from **current** entitlements (intersect locations; preserve/intersect NodeScope — never widen) |

### Catalog

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/catalog` | **Required**: Bearer license token or access ticket | Signed node/location catalog |
| GET | `/locations` | **Required**: same as catalog | Location list (filtered) |
| GET | `/nodes` | **Required**: same as catalog | Node registry view (filtered) |

Unauthenticated catalog/locations/nodes → `401`. Invalid license/ticket → `401`. Catalog is Ed25519-signed; clients verify before trusting node keys/SPKI.

### Node Operations

| Method | Path | Description |
|--------|------|-------------|
| POST | `/nodes/{node_id}/health` | Node heartbeat (`UpdateNodeHealth` only — does not overwrite SPKI/config) |
| POST | `/nodes/{node_id}/drain` | Enable draining mode |
| POST | `/nodes/{node_id}/maintenance` | Maintenance mode |

Node heartbeats use node service identity, not user tokens.

### Revocation

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/revocation` | **Required**: node service identity (`X-Node-ID` + node bearer). User license tokens are rejected (`403`) | Revocation list (JTIs, licenses, devices) |

### Other

| Method | Path | Description |
|--------|------|-------------|
| GET | `/version` | Protocol version info |
| POST | `/master/access` | Master role access (normal auth flow) |

## Node Authentication

Nodes authenticate to Control Plane with unique service identity (Ed25519-signed node tokens / headers). User license credentials are NOT accepted for node heartbeat or revocation sync.

## Catalog Security

1. HTTPS delivery
2. Caller authentication (license or ticket)
3. Ed25519 catalog signature verified client-side
4. Location scoping + `test_only` filtering by role

## test_only Filtering

Control Plane filters test nodes from user catalog responses. Master role receives test + production nodes.

See `core/controlplane/api` for Go type definitions.