# Legacy upgrade (server ≤1.0.4 → 1.0.5+)

## Why bootstrap exists

On nodes still running **nyxveilctl 1.0.3 / 1.0.4**:

```bash
serv_update  →  exec /usr/local/sbin/nyxveilctl update
```

That runs the **legacy updater**, which can leave `tls.key` as `root:root` on failed rollback.

The fixed updater lives inside **nyxveilctl 1.0.5**. You must install that CLI first, without stopping the server or touching TLS.

## Production path (1.0.3 → 1.0.5)

### 1) Trusted CLI-only bootstrap

Download the helper from the signed `server-v1.0.5` release tree (or offline tarball), verify it via `SHA256SUMS`, then:

```bash
sudo bash bootstrap-cli-update.sh --version 1.0.5 --then-update
```

Or two steps:

```bash
sudo bash bootstrap-cli-update.sh --version 1.0.5
# asserts: nyxveilctl == 1.0.5, nyxveil-server still 1.0.3, healthy, TLS unchanged
sudo /usr/local/sbin/nyxveilctl update
# or: sudo serv_update
```

**Trust checks (fail-closed):**

1. Download `release-manifest-linux-$ARCH.json`
2. Verify Ed25519 signature with embedded Nyxveil release public key (`PUB_HEX` / `UpdatePublicKey`)
3. Require `version == 1.0.5` and matching arch
4. Download **only** the `nyxveilctl` asset
5. Verify SHA-256
6. Atomic install to `/usr/local/sbin/nyxveilctl` (temp + fsync + rename, `0755` `root:root`)

On any failure, the previous `nyxveilctl 1.0.3` remains intact. Server / TLS / `server.json` / identity are never touched in this phase.

### 2) Full update with fixed updater

After CLI bootstrap, `nyxveilctl update` (1.0.5) replaces server+ctl, snapshots/restores TLS metadata, enforces `nyxveil:nyxveil` ownership, and requires stable control-socket health.

## After you are on 1.0.5+

```bash
sudo serv_update
```

No bootstrap required — the installed updater is already fixed.

`serv_update_bootstrap` remains available as:

```bash
sudo serv_update_bootstrap
# → nyxveilctl bootstrap-cli --version 1.0.5 --then-update
```

## Offline

```bash
sudo bash bootstrap-cli-update.sh \
  --version 1.0.5 \
  --manifest ./release-manifest-linux-amd64.json \
  --ctl-file ./nyxveilctl-linux-amd64 \
  --then-update
```
