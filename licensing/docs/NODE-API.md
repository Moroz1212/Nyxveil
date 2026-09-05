# Node API (NVP/1)

Base path: `/api/v1`

Node authentication (after registration):

1. **Frozen Core `nodeauth`** — Bearer `<unix>.<sig>` over `nvp-node-v1|node_id|unix` (Ed25519). Control Plane stores `LastCoreTokenUnix` and rejects replays with atomic `UPDATE ... WHERE LastCoreTokenUnix IS NULL OR LastCoreTokenUnix < @new`.
2. Optional legacy HMAC bearer (`nvpnode_*`) when `NodeAuth:AllowLegacyBearer=true`
3. Optional Ed25519 request signature headers (`X-Node-Signature`, …)

Always send `X-Node-Id` (or route/body `node_id`).

Interop gate: `scripts/test-core-interop.ps1` (`NODETOKEN_GO_TO_CS`).

## Endpoints (overview)

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| POST | `/nodes/register` | Bootstrap token in body | Creates node; may return legacy bearer once if enabled |
| POST | `/nodes/heartbeat` | Node auth | **Excluded from aggressive rate limits** |
| GET | `/nodes/config` | Node auth | Enabled / draining / maintenance / policy |
| GET | `/revocations` | Node auth | Snapshot of revoked jtis / licenses / devices |
| … | other node ops | Node auth | As implemented in Api controllers |

## Heartbeat

Nodes should heartbeat about every `NodeHeartbeat:IntervalSeconds` (default 30).

Heartbeat updates **only** dynamic health fields (`CurrentSessions`, `LastSeenAt` = Control Plane receive time, optional reported capacity capped by `NodeConfig.Capacity`, `NodeHealth` / metrics). It never changes Enabled / Draining / MaintenanceMode / TestOnly / identity / ServerVersion / ConfigVersion.

Control Plane workers mark runtime status:

- **Degraded** after `DegradedAfterSeconds` (default 90)
- **Offline** after `OfflineAfterSeconds` (default 180)

`NodeConfig.MaintenanceMode` is independent of runtime status.

## Config synchronization (polling)

Authoritative managed settings live in **NodeConfig**. Future VPN `server/` should:

1. Heartbeat and read `config_version` from the response
2. If `localConfigVersion != config_version`, call `GET /api/v1/node/config`
3. Apply Enabled / Draining / MaintenanceMode / Capacity / policy locally

No push/message-broker is required for v1.

## Maintenance

Admin Enter Maintenance sets `NodeConfig.MaintenanceMode=true` (ConfigVersion++). Catalog projects `Draining=true` so Frozen Core selectors exclude the node without Core changes. Exit Maintenance clears only MaintenanceMode — it does not re-enable a previously disabled node.

## Master role

Master privilege is **`License.Role == master`**, never Plan code. Catalog TestOnly nodes are visible **only** to role `master` (not `test`).

Nodes must treat `master` as a normal role with elevated permissions — not a protocol bypass.
