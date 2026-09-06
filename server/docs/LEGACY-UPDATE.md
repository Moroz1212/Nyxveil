# Legacy upgrade (server ≤1.0.4 → 1.0.7+)

## Why bootstrap exists

On nodes still running **nyxveilctl 1.0.3 / 1.0.4**:

```bash
serv_update  →  exec /usr/local/sbin/nyxveilctl update
```

That runs the **legacy updater**, which can leave `tls.key` as `root:root` on failed rollback.

The fixed updater lives inside **nyxveilctl 1.0.6+**. You must install that CLI first, without stopping the server or touching TLS.

> **Note:** `server-v1.0.5` published `bootstrap-cli-update.sh` with Windows CRLF (`set: pipefail` failure). Use **1.0.6+** bootstrap assets (Unix LF only). Prefer **1.0.7**.

## Production path (1.0.3 → 1.0.7)

```bash
# Verify SHA256SUMS entry for bootstrap-cli-update.sh, then:
sudo bash bootstrap-cli-update.sh --version 1.0.7 --then-update
```

Or two steps:

```bash
sudo bash bootstrap-cli-update.sh --version 1.0.7
# asserts: nyxveilctl == 1.0.7, server still old, healthy, TLS unchanged
sudo /usr/local/sbin/nyxveilctl update
```

**Trust checks (fail-closed):** signed manifest (Ed25519), version/arch match, `nyxveilctl` SHA-256, atomic CLI install. Server / TLS / config untouched in bootstrap phase.

## After you are on 1.0.6+

```bash
sudo serv_update
```

## Offline

```bash
sudo bash bootstrap-cli-update.sh \
  --version 1.0.7 \
  --manifest ./release-manifest-linux-amd64.json \
  --ctl-file ./nyxveilctl-linux-amd64 \
  --then-update
```
