# Failover

## Transport Failover

```
QUIC/UDP:443 (primary)
    → failure after 250ms delay
TLS/TCP:443 (fallback)
    → failure
Next endpoint / next node (same location only)
```

Implemented via `transport.Registry.DialWithRacing` with configurable Happy-Eyeballs-like delay.

**No insecure downgrade** — certificate validation always enforced.

## Multi-Node Failover (same location only)

Within the same `DesiredLocationID` / `LocationID` (`failover.ConnectWithFailover` / production connector):

1. Client verifies catalog once and loads healthy candidates for **that location only**
2. On connection failure, try next candidate (up to `MaxNodeAttempts`, default 3)
3. Try alternate endpoints on the same node; transport racing is QUIC then TLS per attempt

Transport failover and node failover compose: each node attempt may race QUIC→TLS before moving to the next node.

**Core never automatically switches locations.** If the whole location is unavailable, the application must call `OpenSession` again with a new `DesiredLocationID` / `LocationID`.

## Ticket Scope

| Kind | How issued | Failover behavior |
|------|------------|-------------------|
| **Location-scoped (default)** | `location_id` set, `node_id` empty → JWT `Locations` set, `NodeScope` empty | Any healthy node in `Locations` (same-location multi-node) |
| **Node-scoped (optional restrictive)** | `node_id` set → `NodeScope=[node_id]` | Connector peeks `NodeScope` into `AllowedNodeIDs`; other nodes are not dialed |

Ticket refresh (`POST /ticket/refresh`) rebuilds from **current** entitlements: never widens location or node scope; preserves `NodeScope` when policy is unrestricted. See [AUTH-ARCHITECTURE.md](AUTH-ARCHITECTURE.md).

## Exhausted attempts (typed error)

When all node/transport attempts fail, `failover.ConnectWithFailover` returns `*failover.ExhaustedError`:

- `errors.Is(err, nvperr.ErrTransportUnavailable)` or `nvperr.ErrNoHealthyNodes`
- `TriedNodes` lists **node IDs only** (no tickets, keys, or other secrets)
- Connector `OpenSession` surfaces this error without re-wrapping away the type

## Cross-Location

Not performed by Core automatic failover. Application selects another allowed location and opens a **new** session (`OpenSession` with a new location).

## Health Scoring Factors

- Latency (recent measurements)
- Recent failure count
- Server capacity / current load
- Maintenance / draining flags
- Control Plane health status

## IP Block / Endpoint Failure

Node descriptors support multiple endpoints. Catalog updates from Control Plane provide new IPs without client app update.

**Does NOT guarantee bypass of all network filters.**

## Mobile Network Change

QUIC supports connection migration where the stack allows. Session architecture tolerates NAT rebinding via the transport layer.

## NOT VERIFIED

- Real-world TSPU/operator blocking scenarios
- Cross-country failover latency under censorship

Independent security/cryptographic audit: **NOT PERFORMED**
