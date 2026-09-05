# Nyxveil Server (VPN Node)

Production Linux node for Nyxveil — registers with Control Plane, terminates TLS/QUIC, and bridges client traffic over TUN.

## One-command install (Ubuntu 24.04)

```bash
curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash
```

The installer is **self-contained** (systemd units are embedded; no repo checkout required). It prompts for Control Plane URL, location, display name, and bootstrap token (`read -s`). Token is never written to disk.

Downloads are **fail-closed**: the installer fetches `release-manifest-linux-${arch}.json` for tag `server-v*`, verifies the Ed25519 signature (same public key as the updater), then verifies each asset SHA-256.

### Pinned Control Plane (42mou.ru)

```bash
sudo bash install.sh --control-plane https://42mou.ru:8443 \
  --control-plane-ca-file /path/to/cp-ca.pem \
  --location hel-1 \
  --name "hel-edge-01" \
  --public-host vpn.example.com \
  --bootstrap-token "$TOKEN"
```

Optional SPKI pin (**SelfSignedPinned**, no CA file required):

```bash
  --control-plane-spki-pin <hex-sha256-of-peer-spki>
```

`PUBLIC_HOST` or `PUBLIC_IP` is required in production. Use `--test-self-signed` only for lab installs.

With flags (curl|bash):

```bash
curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash -s -- \
  --control-plane https://control.example.com \
  --location hel-1 \
  --name "hel-edge-01" \
  --public-host vpn.example.com
```

## Offline / local binary-dir

Build or unpack a release, then:

```bash
sudo ./installer/install.sh \
  --binary-dir ./dist/release/linux-amd64 \
  --skip-download \
  --control-plane https://control.example.com \
  --location hel-1 \
  --name "hel-edge-01" \
  --public-host vpn.example.com \
  --bootstrap-token "$TOKEN"
```

No remote signature check when using `--binary-dir` / `--skip-download`. No Go toolchain is required on the VPS.

## Paths

| Path | Purpose |
|------|---------|
| `/etc/nyxveil/server.json` | Non-secret config (`pinned_ca_file`, `control_plane_spki_pin`, …) |
| `/var/lib/nyxveil/` | `node.key`, TLS material, applied config |
| `/run/nyxveil/` | Control socket |
| `/usr/local/sbin/` | `nyxveil-server`, `nyxveilctl` |
| `/etc/nftables.d/nyxveil.conf` | Isolated `inet nyxveil` table |
| `nyxveil-firewall.service` | Oneshot: `nft -f` on start, delete table on stop |

## Binaries

- `cmd/nyxveil-server` — node daemon; `nyxveil-server --register-stdin` for Control Plane bootstrap (empty token + existing `node.key` = PoP repair)
- `cmd/nyxveilctl` — status/health/start/stop/logs

After install: `serv_status`, `serv_health`, `serv_logs -f`, …

## Docs

See [docs/](docs/) — start with [INSTALL.md](docs/INSTALL.md) and [ARCHITECTURE.md](docs/ARCHITECTURE.md).

Frozen Protocol Core notes: [THIRD_PARTY_CORE.md](THIRD_PARTY_CORE.md).

## Module

```
github.com/nyxveil/server
```
