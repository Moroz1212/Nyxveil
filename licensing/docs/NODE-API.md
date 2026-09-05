# Node management API 1.0.0

This management contract is **nvp-node-req-v2**. Frozen NVP/1 wire, authentication,
transport and failover contracts remain unchanged.

## Signed requests

Normal heartbeat, config and revocation calls require Ed25519 request signatures.
Reusable bearers, timestamp-only Core tokens and req-v1 signatures are rejected.
Send exactly one of each header:

- `X-Node-Id`: registered identity, case-sensitive, 1–128 ASCII letters/digits or `-_.`.
- `X-Node-Timestamp`: nonnegative Unix seconds in canonical decimal, no leading zeros.
- `X-Node-Nonce`: fresh cryptographically random 16–64 bytes, unpadded base64url.
- `X-Node-Signature`: 64-byte Ed25519 signature, unpadded base64url.

Sign these UTF-8 bytes, with literal ASCII `|` separators and **no trailing newline**:

```text
nvp-node-req-v2|node_id|unix_ts|nonce|METHOD|canonical_path_query|body_sha256
```

`METHOD` is the actual uppercase HTTP method. The canonical path/query is the
ASP.NET request `PathBase.ToUriComponent() + Path.ToUriComponent() + QueryString.ToUriComponent()`.
Use the canonical ASCII endpoint paths below. Include the query's leading `?`,
original parameter order, duplicates, escaping and `+` characters. Do not sort or
decode the query. A reverse proxy must preserve this target, or the node must sign
the target received by Control Plane. `X-Node-Method` and `X-Node-Path` are ignored.

`body_sha256` is lowercase 64-character hex SHA-256 of **exact transmitted bytes**,
including JSON whitespace, UTF-8 characters and any trailing newline. Empty GET /
DELETE bodies use SHA-256(empty),
`e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
Node endpoints accept JSON object bodies with a 65,536-byte maximum. Hashing and
identity checks run before MVC model binding; the buffered stream is rewound.

All supplied route, query and body `node_id` / `NodeId` values must exactly equal
the authenticated `X-Node-Id`, including repeated properties and parameters.
Identity mismatches return 403; bad/missing signatures and replays return 401;
oversized bodies return 413. A valid signature cannot authorize another node.

Timestamp tolerance is ±300 seconds. MSSQL `NodeRequestNonces` has composite PK
`(NodeId, NonceHash)`, where NonceHash is lowercase SHA-256 of decoded nonce bytes.
The atomic insert is required before dispatch. Concurrent identical requests yield
one acceptance. A rejected/retried operation needs a fresh nonce. Entries expire
at signed timestamp +301 seconds, including the full future-skew validity window;
the retention worker removes expired rows every six hours. Restarting or running
multiple Control Plane instances does not reset replay state.

## Endpoints

- `POST /api/v1/nodes/register`: bootstrap for a new identity; existing identity requires explicit Core PoP as below.
- `POST /api/v1/nodes/{nodeId}/health`: req-v2 heartbeat.
- `POST /api/v1/node/heartbeat`: req-v2 heartbeat alias.
- `GET /api/v1/node/config`: req-v2 authoritative config.
- `GET /api/v1/revocation`: req-v2 complete revocation snapshot; never truncated.

## Registration and compatibility PoP

Frozen `nvp-node-v1` (`unix.signature` over `nvp-node-v1|node_id|unix`) is accepted
only as the body `node_token` proof on an existing-node registration retry. The
existing credential public key verifies the proof; the request must retain both
the registered identity and public key. Bootstrap cannot reset an existing node.
Core PoP uses atomic `LastCoreTokenUnix` advancement and is deliberately limited
to this low-frequency operation. Use a later timestamp for a fresh registration
retry. Normal req-v2 requests do not use this counter, so heartbeat → config in
the same second succeeds with independent nonces, without sleeps.

`NodeAuth:AllowLegacyBearer` remains false by default. Its historical opt-in only
controls legacy credential issuance; it cannot enable bearer auth on normal APIs
or registration PoP. Registration does not implement key rotation. Rotation
requires a separate authenticated/admin-approved operation in a later release.

## Authoritative config and future server flow

NodeConfig is the source for Enabled, Draining, MaintenanceMode, Capacity and
transport/ECH/MTU/version policies. The registry owns identity, TestOnly and the
canonical LocationId. Config responses join the current registry location as
`location_id`; both initial registration and config pulls return it.

Future server sequence:

1. Heartbeat (normally every 30 seconds), read `config_version=N`.
2. If the local version differs, immediately GET `/api/v1/node/config` with a new nonce.
3. Validate and apply the complete config atomically: `location_id`, Enabled,
   Draining, MaintenanceMode, Capacity and all supplied policies.
4. Update Frozen AuthHandler's expectedLocationID to `location_id` together with
   that config. Stop admitting new sessions while disabled/draining/in maintenance.
5. Store the applied version only after successful application, then continue.

A FI→DE admin change increments the version once and the next pull returns the
canonical DE LocationId, never Location.Code. Each successful managed change uses
a database compare-and-swap on ConfigVersion, increments once, updates the Node
projection and writes AuditLog in one transaction. A stale concurrent change
returns a conflict and rolls back; reload and explicitly retry. Audit failure
also rolls back both config and projection.

Heartbeat changes runtime health/metrics only. LastSeenAt uses Control Plane
`IClock.UtcNow`, never node timestamps. Reported capacity is capped by admin
capacity, including zero. Heartbeat cannot alter managed fields or ConfigVersion.
Maintenance projects as `Draining=true` in the catalog for Frozen selection.
TestOnly access requires an active valid license with `License.Role == "master"`;
neither a master plan nor the test role grants access. Catalog ticket callers are
rechecked against current license/device/revocation state. Refresh intersects
old/current permissions and never upgrades role; a role upgrade needs a new session.

## Verification

Run `scripts/test-core-interop.ps1 -FrozenZip <authoritative-zip>` and
`scripts/test-management-interop.ps1`. The independent Go management signer uses
the standard library and does not modify/import Frozen Core. HTTP security tests
exercise actual requests against a relational SQLite test store. SQL Server model,
migration and bootstrap DDL alignment is checked separately; live MSSQL races and
Windows Service installation must also be exercised on the deployment VM.
