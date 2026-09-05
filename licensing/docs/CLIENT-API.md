# Client API (NVP/1)

Base path: `/api/v1`

Frozen Core contracts apply: location-scoped tickets (`aud=nvp-node`), refresh never widens scope, device-bound authentication, signed catalog.

## Endpoints (overview)

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| POST | `/license/validate` | License token | Validate license without issuing ticket |
| POST | `/device/activate` | License token | Enforce MaxDevices (serializable / row locks) |
| POST | `/device/remove` | License token | Remove device binding |
| POST | `/ticket/issue` | License + device | Issues location-scoped access ticket (`aud=nvp-node`) |
| POST | `/ticket/refresh` | License + prior ticket | **Must not widen** locations/node scope |
| GET | `/catalog` | License bearer | Signed node catalog filtered by entitlement |
| GET | `/locations` | As implemented | Location list (`location_id`) |
| GET | `/nodes` | As implemented | Node descriptors for client |
| GET | `/version` | Public/minimal | Control Plane / API version |

## License token format

Public id form: `nyx_lic_<hex>`. Raw secret is shown once at creation and never stored.

## Access tickets

- Audience **`nvp-node`** (Frozen Core)
- Short-lived (default TTL from `Tickets:TtlMinutes`, typically 15)
- Include location scope and node scope derived from license + catalog rules
- Permission `connect` required for VPN session
- Cross-location access requires a **new** OpenSession/ticket — not failover

## Rate limits

Sensitive client paths are rate-limited by IP. Configure via `RateLimits`.
