# Troubleshooting

## Service won’t start

```bash
systemctl status nyxveil-server -l
journalctl -u nyxveil-server -e --no-pager
```

Common causes:

- Missing `/etc/nyxveil/server.json` or empty `control_plane_url` / `node_id`
- Missing capabilities / TUN (`/dev/net/tun`)
- Port 443 already bound
- TLS cert/key paths invalid (`tls_cert_file` / `tls_key_file`)

## Registration failed

- Verify bootstrap token is unused and Control Plane URL is reachable
- Check DNS and outbound HTTPS
- Confirm `location_id` exists on Control Plane
- Token must not appear in `server.json` (if it does, rotate token and scrub file)

## `nyxveilctl health` fails

- Socket `/run/nyxveil/control.sock` — unit must be running as user `nyxveil` with `RuntimeDirectory=nyxveil`
- SELinux/AppArmor rarely blocks unix sockets on stock Ubuntu 24.04; check journal

## Clients connect but no internet

```bash
sysctl net.ipv4.ip_forward
nft list table inet nyxveil
ip addr show nyxveil0
```

Ensure masquerade matches `vpn_subnet_cidr` and forwarding is allowed.

## Repair after partial install

Re-run installer; it preserves `node.key` and `node_id`. If identity is corrupted and CP still has the old public key, coordinate a Control Plane re-key / re-register.

## Uninstall leftovers

```bash
sudo ./installer/uninstall.sh --purge-state --purge-user
nft list tables   # inet nyxveil should be gone
```
