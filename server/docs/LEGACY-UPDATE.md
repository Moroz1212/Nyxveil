# Legacy / blocked upgrades

## Why bootstrap exists

On nodes still running **nyxveilctl 1.0.3 / 1.0.4**:

```bash
serv_update  →  exec /usr/local/sbin/nyxveilctl update
```

That runs the **legacy updater**, which can leave `tls.key` as `root:root` on failed rollback.

The fixed updater lives inside **nyxveilctl 1.0.6+**. You must install that CLI first, without stopping the server or touching TLS.

> **Note:** `server-v1.0.5` published `bootstrap-cli-update.sh` with Windows CRLF (`set: pipefail` failure). Use **1.0.6+** bootstrap assets (Unix LF only).

## Production path (1.0.7 → 1.0.9) — management-plane deadlock

Nodes that are **dataplane healthy** but `cp_connected=false` / `healthy=false` (e.g. stale `control_plane_url`) cannot install 1.0.8 with the **1.0.7 updater**: it incorrectly requires global `healthy=true`.

**Do not run `serv_update` on 1.0.7 first.** Bootstrap CLI to 1.0.9, then update:

```bash
# Verify SHA256SUMS entry for bootstrap-cli-update.sh, then:
sudo bash bootstrap-cli-update.sh --version 1.0.9
# server stays 1.0.7; only /usr/local/sbin/nyxveilctl becomes 1.0.9

sudo nyxveilctl update
# commits 1.0.9 when dataplane OK even if CP still disconnected (preexisting)

sudo nyxveilctl configure --check --control-plane-url https://cp.nyxveil.ru:18443
sudo nyxveilctl configure --control-plane-url https://cp.nyxveil.ru:18443
```

Or with an already-present ctl that supports bootstrap:

```bash
sudo nyxveilctl bootstrap-cli --version 1.0.9
sudo nyxveilctl update
```

## Production path (1.0.3 → 1.0.9)

```bash
# Verify SHA256SUMS entry for bootstrap-cli-update.sh, then:
sudo bash bootstrap-cli-update.sh --version 1.0.9 --then-update
```

**Trust checks (fail-closed):** signed manifest (Ed25519), version/arch match, `nyxveilctl` SHA-256, atomic CLI install. Server / TLS / config untouched in bootstrap phase.

## After you are on 1.0.9+

```bash
sudo serv_update
# or: sudo nyxveilctl update
```

## Offline

```bash
sudo bash bootstrap-cli-update.sh \
  --version 1.0.9 \
  --manifest ./release-manifest-linux-amd64.json \
  --ctl-file ./nyxveilctl-linux-amd64 \
  --then-update
```
