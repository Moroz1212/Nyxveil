# Control Plane 1.0.6 — same-node catalog metadata refresh

## Defect fixed

Authenticated same-node re-register previously returned success **without updating**
DB advertisement fields (`ServerVersion`, `ServerName`, `SpkiPin`, endpoints).
Heartbeat only refreshed `LastSeen` / health / sessions — so the signed catalog kept
stale registration columns (e.g. server_version 1.0.1, old SPKI, IP as server_name).

Duplicate catalog endpoints came from registering two rows for the same host+port
(TLS+QUIC on 443) and projecting each as an identical multi-profile endpoint.

## Changes

- Existing-node PoP path updates mutable node-owned ads; preserves NodeId, LocationId,
  Enabled/TestOnly/Draining, identity, credentials, secrets
- Rejects location self-migration
- Endpoint dedupe on register + catalog projection (`Host|Port|IpFamily`)
- Catalog continues live DB projection (no stale cache); re-sign after metadata change
- Version API default: 1.0.6

## Preserve on upgrade

- Local listen **8443**
- External NAT **18443 → 8443**
- Hostname **cp.nyxveil.ru**
- SystemTrust / Let's Encrypt certificate (do not re-issue unless needed)
- Existing DB, licenses, ticket signer, catalog signer, node identities
- Restart **only** `NyxveilControlPlane`

## Deploy (Windows CP host)

```powershell
# From unpacked release (or publish folder):
powershell -ExecutionPolicy Bypass -File .\scripts\update-windows.ps1 -PublishDir .\publish
```

No DB schema migration required for this patch.

## Post-deploy verification (operator)

1. On Ubuntu node (manual — do not automate from this agent):

```bash
sudo nyxveilctl configure --control-plane-url https://cp.nyxveil.ru:18443
# or same-node re-register path already used live
```

2. Confirm signed catalog for `nv-test-227e939e`:

- `server_version` = current node (e.g. 1.0.10 / 1.0.11 after server update)
- `server_name` = `fi-hel-01.nyxveil.ru` (TLS FQDN / PublicHost)
- `spki_pin` = `Y4VRkarf5cFKyESDcgYl5Fkl1UI4gaoxcfXFMVdsRIg=`
- `location_id` = `fi-helsinki`
- endpoints: no duplicate identical host+port rows

3. `GET /api/v1/catalog-keys` remains compatible.
