# Existing-node reconfigure (`nyxveilctl configure`) — server-v1.0.4

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

## ACME transaction order (v1.0.4)

1. Validate CLI / merge (identity preserved)
2. DNS check for `--tls-domain` (fail closed — **no** config/TLS mutation on mismatch)
3. Snapshot `server.json`, live TLS, Nyxveil nftables
4. Open TCP/80 in `inet nyxveil` only (old TLS stays live)
5. Issue ACME into **staging** (`tls.next.crt` / `tls.next.key`); reuse live leaf key when present
6. Validate **staged** cert for target FQDN (never against live self-signed IP cert)
7. Stop → atomic commit live TLS + `server.json` → start → health → same-node PoP re-register
8. On any failure: restore snapshots + firewall; restart previous working service (`rolled_back=true`)

## SPKI

If leaf SPKI changes after commit, Control Plane is updated via existing `POST /api/v1/nodes/register` with NodeToken PoP (same `node_id`). CP failure rolls back live TLS/config.

## Upgrade

Publish `server-v1.0.4`, then on the node:

```bash
sudo serv_update
```

Then run `serv_configure` as above. Do not hand-edit `/etc/nyxveil/server.json`.
