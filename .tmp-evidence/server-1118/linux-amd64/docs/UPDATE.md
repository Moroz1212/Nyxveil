# Update

## Release artifacts

Built by `scripts/build-release.sh` / packaged by `scripts/package-release.sh`:

- `nyxveil-server-linux-amd64` / `arm64`
- `nyxveilctl-linux-*`
- `SHA256SUMS`
- Optional tarball layout with installer + unit + firewall

GitHub Release tag convention: `server-vX.Y.Z` (matches `server/VERSION`).

## Manual binary replace

```bash
systemctl stop nyxveil-server
install -m 0755 nyxveil-server /usr/local/sbin/nyxveil-server
install -m 0755 nyxveilctl /usr/local/sbin/nyxveilctl
systemctl start nyxveil-server
nyxveilctl health
```

Preserve `/etc/nyxveil/server.json` and `/var/lib/nyxveil/node.key`.

## Re-run installer (repair)

Re-running `install.sh` detects existing `node.key` / `node_id` and preserves them while refreshing binaries, unit, sysctl, and nftables table.

## CLI update hook

```bash
nyxveilctl update https://example.com/manifest.json
```

Manifest schema is implemented in `internal/updater` (SHA256 + Ed25519 signature). Production signing key must replace the placeholder before enabling automated updates.

## Rollback

Updater keeps previous binary at `/var/lib/nyxveil/nyxveil-server.prev` and a rollback marker when health checks fail after replace.
