# Install

Target: **Ubuntu 24.04**, **systemd**, **amd64 or arm64**, **nftables**. No Docker. No `go build` on the VPS.

`installer/install.sh` is **curl|bash self-contained**: systemd units are embedded as heredocs. A sibling `systemd/` directory is not required.

## Prerequisites

- Root (`sudo`)
- `/dev/net/tun` available
- Outbound HTTPS to Control Plane
- ~700MB+ RAM recommended (installer warns below that)
- ≥200MB free on `/var` or `/`
- `PUBLIC_HOST` or `PUBLIC_IP` (required in production)

## Interactive (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash
```

Prompts (token via `read -s`):

- Control Plane URL
- Location ID
- Display name
- Bootstrap token (skipped on repair when `node.key` + `node_id` already exist — PoP re-register)

## Pinned install (42mou.ru)

```bash
sudo bash install.sh --control-plane https://42mou.ru:8443 \
  --control-plane-ca-file /path/to/cp-ca.pem \
  --location hel-1 \
  --name hel-edge-01 \
  --public-host vpn.example.com \
  --bootstrap-token "$TOKEN"
```

Also supported: `--control-plane-spki-pin <64-hex>` (**SelfSignedPinned**). The pin is the trust anchor — no CA PEM file is required. Installer and Go runtime both enforce SPKI + hostname/SAN + certificate validity (no TrustAll). Use `--control-plane-ca-file` for **PinnedCA** mode instead.

## Flagged install

```bash
sudo bash install.sh \
  --control-plane https://control.example.com \
  --location hel-1 \
  --name hel-edge-01 \
  --bootstrap-token "$TOKEN" \
  --public-host vpn.example.com \
  --tls-port 443 \
  --quic-port 443
```

## Offline / air-gapped

On a build host:

```bash
cd server
bash scripts/build-release.sh
bash scripts/package-release.sh   # signs release-manifest-linux-*.json
```

Copy `dist/release/linux-amd64` (or arm64) to the VPS:

```bash
sudo ./installer/install.sh --binary-dir . --skip-download \
  --control-plane https://control.example.com \
  --location hel-1 \
  --name hel-edge-01 \
  --public-host vpn.example.com \
  --bootstrap-token "$TOKEN"
```

## Release verification (downloads)

Fail-closed when not using `--binary-dir`:

1. Download `release-manifest-linux-${arch}.json` from GitHub release `server-v${VERSION}`
2. Verify Ed25519 signature over canonical JSON bytes (same as Go `updater.CanonicalManifestBytes`) with openssl PureEd25519 (`pkeyutl -rawin -verify`)
3. Download each asset; verify SHA-256; install

Missing/invalid manifest, signature, or checksum → installer exits nonzero (no WARN skip).

## What the installer does

1. Validates OS, systemd PID 1, arch, TUN, RAM/disk
2. TLS-prechecks Control Plane
3. `apt` installs only missing: `nftables`, `iproute2`, `ca-certificates`, `curl`, `openssl`, `jq`
4. Creates user `nyxveil` and directories
5. Installs binaries to `/usr/local/sbin`
6. Writes `sysctl.d/99-nyxveil.conf` (`ip_forward=1`)
7. Applies **only** nftables table `inet nyxveil` (never flushes ruleset)
8. Installs `nyxveil-firewall.service` (oneshot) + `nyxveil-server.service` (`After=`/`Wants=` firewall)
9. Writes `/etc/nyxveil/server.json` **without** the token (`pinned_ca_file` / `control_plane_spki_pin` when given)
10. Registers via `nyxveil-server --register-stdin` (empty token on repair = PoP)
11. Enables and starts systemd; health-gates `nyxveilctl health` up to 60s
12. **COMMIT** — EXIT trap no longer rolls back

On failure before COMMIT, the EXIT trap restores prior binaries/units/config and removes incomplete state. Repair preserves `node.key` and `node_id`.

## Uninstall

```bash
sudo ./installer/uninstall.sh              # keep state
sudo ./installer/uninstall.sh --purge-state --purge-user
```

## After install

```bash
serv_status
serv_health
serv_logs -f
```
