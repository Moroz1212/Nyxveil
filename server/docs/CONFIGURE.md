# Existing-node reconfigure (`nyxveilctl configure`) — server-v1.0.3

Transactional, idempotent reconfiguration of an **already registered** VPN node.
Preserves `node_id`, `node.key`, and Control Plane identity. Never requires a bootstrap token.

## Commands

```bash
# Dry-run (no changes)
sudo nyxveilctl configure --check \
  --public-host fi-hel-01.nyxveil.ru \
  --tls-domain fi-hel-01.nyxveil.ru \
  --tls-email admin@example.com \
  --dns-servers 1.1.1.1,1.0.0.1 \
  --expect-public-ip 46.8.218.27

# Apply ACME + public_host + DNS (existing node)
sudo nyxveilctl configure \
  --public-host fi-hel-01.nyxveil.ru \
  --tls-domain fi-hel-01.nyxveil.ru \
  --tls-email admin@example.com \
  --dns-servers 1.1.1.1,1.0.0.1 \
  --expect-public-ip 46.8.218.27

# Alias
sudo serv_configure --status
```

`--expect-public-ip` is required for ACME when `public_host` is already an FQDN (DNS must resolve to this IP **before** any TLS change).

## Behavior

1. Snapshot `server.json`, TLS material, Nyxveil nftables file
2. DNS check for `--tls-domain` (fail closed — no config/TLS mutation on mismatch)
3. Stop service → apply nftables (`inet nyxveil` only; open TCP/80 for HTTP-01 if ACME) → atomic `server.json` → ACME or operator cert → validate leaf → PoP re-register (same NodeId) → start → `nyxveilctl health`
4. On any failure: restore snapshots, restore firewall, restart previous working service

## SPKI

If leaf SPKI changes, Control Plane is updated via existing `POST /api/v1/nodes/register` with NodeToken PoP (same `node_id`). Failure rolls back.

## Upgrade

Publish `server-v1.0.3`, then on the node:

```bash
sudo serv_update
```

Then run `serv_configure` as above. Do not hand-edit `/etc/nyxveil/server.json`.
