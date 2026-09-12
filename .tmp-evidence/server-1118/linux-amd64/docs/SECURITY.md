# Security

## Host hardening (defaults)

systemd unit `nyxveil-server.service`:

- `User=nyxveil` (non-root)
- `AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE` only
- `NoNewPrivileges=true`
- `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`
- `ReadWritePaths` limited to `/var/lib/nyxveil` and `/run/nyxveil` (`/etc/nyxveil` is read-only for the daemon)
- Dynamic CP config in `/var/lib/nyxveil/applied-config.json`; static bootstrap in `/etc/nyxveil/server.json`
- Registration runs as `nyxveil` so `node.key` / `tls.key` are never root-owned 0600 leftovers
- `DeviceAllow=/dev/net/tun rw`
- `Restart=on-failure`

## Secrets

| Secret | Storage | Notes |
|--------|---------|-------|
| Bootstrap token | Memory / argv once | Never in `server.json` |
| `node.key` | `/var/lib/nyxveil` 0600 | Ed25519; backup offline if needed |
| TLS key | `/var/lib/nyxveil/tls.key` | Protect like node.key |
| Client tickets | Ephemeral | Verified via protocol core |

## Network

- Isolated nftables table — do not flush host ruleset
- Control socket local-only
- Prefer HTTPS Control Plane with modern TLS

## Supply chain

- Release binaries verified with `SHA256SUMS` when present
- Frozen Protocol Core SHA256 documented in `THIRD_PARTY_CORE.md`
- Do not modify `third_party/nvp` in place

## Operator hygiene

- Rotate bootstrap tokens after use
- Restrict SSH independently of Nyxveil
- Keep Ubuntu 24.04 patched
- Treat `serv_*` / `nyxveilctl` as root-equivalent for service control
