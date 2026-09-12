# Existing-node reconfigure (`nyxveilctl configure`) — server-v1.0.8

Transactional, idempotent reconfiguration of an **already registered** VPN node.
Preserves `node_id`, `node.key`, and Control Plane identity. Never requires a bootstrap token.

## Upgrade

From **1.0.6+** (fixed updater): `sudo serv_update`

From **1.0.3 / 1.0.4**, or broken **1.0.5 CRLF** bootstrap: see `docs/LEGACY-UPDATE.md`.

## Control Plane URL cutover (1.0.8)

When Control Plane moves to a new public hostname (e.g. `42mou.ru` → `cp.nyxveil.ru`):

```bash
sudo nyxveilctl configure --check \
  --control-plane-url https://cp.nyxveil.ru:18443

sudo nyxveilctl configure \
  --control-plane-url https://cp.nyxveil.ru:18443
```

Behaviour:

- Validates target URL with **SystemTrust** (HTTPS only; no InsecureSkipVerify) before any write
- Signed GetConfig probe with existing `node.key` (no bootstrap token)
- Clears legacy `control_plane_spki_pin` so the daemon uses SystemTrust for the public CP
- Atomic `server.json` update; same-node PoP re-register; requires `cp_connected` + catalog/management verify
- Preserves VPN TLS leaf, SPKI, `public_host`, DNS, `node_id`, `location_id`
- Rollback restores previous config; distinguishes config-complete vs old-CP-endpoint-impossible

## ACME leaf-key compatibility (1.0.7+)

Ed25519 self-signed → staged ECDSA P-256; renewals reuse P-256. See prior notes.
