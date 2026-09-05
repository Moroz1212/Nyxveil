# Clean-host test

Validate packaging on a fresh Ubuntu 24.04 VM (no prior Nyxveil).

## Preparation

1. New VPS: Ubuntu 24.04, amd64 or arm64
2. Confirm `ps -p 1 -o comm=` → `systemd`
3. Confirm `/dev/net/tun` exists
4. Have a valid Control Plane bootstrap token

## Online path

```bash
curl -fsSL https://raw.githubusercontent.com/Moroz1212/Nyxveil/main/server/installer/install.sh | sudo bash
```

Complete prompts. Expect:

- Packages installed only if missing
- Service active
- `serv_health` / `nyxveilctl health` succeeds
- `nft list table inet nyxveil` shows masq + ports
- `server.json` has **no** token field
- `/var/lib/nyxveil/node.key` mode 0600

## Offline path

```bash
# on build machine
cd server && bash scripts/build-release.sh && bash scripts/package-release.sh
# copy linux-$ARCH tree to VPS
sudo ./installer/install.sh --binary-dir . --skip-download \
  --control-plane "$CP" --location "$LOC" --name "$NAME" --bootstrap-token "$TOKEN"
```

## Failure / rollback drill

Kill network mid-register or stop after writing config but before COMMIT (inject fault). Confirm EXIT trap removes incomplete unit/binaries and does **not** `nft flush ruleset`.

## Repair drill

```bash
sudo ./installer/install.sh --binary-dir . --skip-download ...
```

Confirm same `node_id` and identical `node.key` fingerprint.

## Uninstall drill

```bash
sudo ./installer/uninstall.sh
sudo ./installer/uninstall.sh --purge-state --purge-user
```

## CI smoke (no VPS)

From `server/` (Linux or Git Bash):

```bash
bash scripts/test-install.sh
```
