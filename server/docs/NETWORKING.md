# Networking

## Listeners

| Transport | Default | Config key |
|-----------|---------|------------|
| TLS (TCP) | `:443` | `tls_listen` |
| QUIC (UDP) | `:443` | `quic_listen` |

Installer flags `--tls-port` / `--quic-port` write these listen addresses.

`public_host` (or `--public-ip` when host omitted) is advertised to Control Plane as **both** TLS and QUIC endpoints during register (ports parsed from listen addresses; default 443).

## TUN datapath

- Interface name: `nyxveil0`
- Node address: first usable address in `vpn_subnet_cidr` (default `10.66.0.0/24`)
- Requires `/dev/net/tun` and `CAP_NET_ADMIN`
- Sysctl: `net.ipv4.ip_forward=1` via `/etc/sysctl.d/99-nyxveil.conf`
- **Fail-closed:** if TUN open or bridge start fails, the node process exits with error unless `--skip-tun` was set explicitly.

## Client network config (TypeConfig)

After AUTH succeeds and the node allocates a VPN IP — **before** `ReadLoop` — the node sends an NVP control message `TypeConfig` (`0x04`) with JSON:

```json
{
  "vpn_ip": "10.66.0.2",
  "vpn_prefix": 24,
  "mtu": 1420,
  "gateway": "10.66.0.1"
}
```

| Field | Meaning |
|-------|---------|
| `vpn_ip` | Client tunnel IPv4 |
| `vpn_prefix` | Subnet prefix length |
| `mtu` | Tunnel MTU |
| `gateway` | Node TUN address (typically `.1`) |

Implemented in `internal/netcfg` (encode/decode) and delivered via `//go:linkname` to Frozen Core `session.sendControl` without editing `third_party/nvp`. Clients should apply the addresses before forwarding user packets. Source IP spoofing is still enforced against the allocated `vpn_ip`.

## NAT

Client subnet is masqueraded on egress through the host’s uplink using nftables table `inet nyxveil` chain `postrouting`. See [FIREWALL.md](FIREWALL.md).

## Control socket

- Linux: Unix socket `/run/nyxveil/control.sock`
- Used by `nyxveilctl status|health`
- Not exposed on the public network

## Ports to allow inbound

- TCP `tls-port` (default 443)
- UDP `quic-port` (default 443)
- Outbound HTTPS to Control Plane

Do not expose the control socket or SSH alternatives via Nyxveil config.
