# Failover

## Transport Failover

```
QUIC/UDP:443 (primary)
    ↓ failure after 250ms delay
TLS/TCP:443 (fallback)
    ↓ failure
Next endpoint / next node
```

Implemented via `transport.Registry.DialWithRacing` with configurable Happy-Eyeballs-like delay.

**No insecure downgrade** — certificate validation always enforced.

## Node Failover

Within same location:
1. Client/Control Plane selects healthy node by health score
2. On connection failure, try next candidate (up to 3 attempts)
3. Try alternate endpoints on same node

## Location Failover

If entire location unavailable:
- Client selects next allowed healthy location per policy
- Automatic — no manual config editing

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

QUIC supports connection migration where stack allows. Session architecture tolerates NAT rebinding via transport layer.

Network chaos tests validate basic resilience.

## NOT VERIFIED

- Real-world TSPU/operator blocking scenarios
- Cross-country failover latency under censorship

Independent security/cryptographic audit: **NOT PERFORMED**
