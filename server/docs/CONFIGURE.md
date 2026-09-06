# Existing-node reconfigure (`nyxveilctl configure`) — server-v1.0.5

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

## TLS ownership contract (v1.0.5)

| Path | Owner | Mode |
|------|-------|------|
| `/var/lib/nyxveil` | `nyxveil:nyxveil` | `0700` |
| `tls.crt` | `nyxveil:nyxveil` | `0644` |
| `tls.key` | `nyxveil:nyxveil` | `0600` |

Snapshots restore **contents + uid/gid/mode**. `serv_update` / `configure` enforce this after commit and rollback. Health requires control socket + stable `/health` (not merely `systemctl is-active`).

## Upgrade

From **1.0.5+**:

```bash
sudo serv_update
```

From **1.0.3 / 1.0.4** (legacy broken updater): see `docs/LEGACY-UPDATE.md` — bootstrap CLI first, then update.

Then run `serv_configure` as above. Do not hand-edit `/etc/nyxveil/server.json`.
