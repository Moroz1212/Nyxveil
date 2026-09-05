# Resource budget

Designed for small VPS edge nodes (no Docker overhead).

## Minimum (installer gates)

| Resource | Threshold | Behavior |
|----------|-----------|----------|
| RAM | &lt; 700 MiB | Warning only |
| Disk (`/var` or `/`) | &lt; 200 MiB free | Install fails |
| CPU | 1 vCPU | Supported; capacity limited |
| Arch | amd64 / arm64 | Required |

## Steady-state expectations (order of magnitude)

| Component | Disk | Notes |
|-----------|------|-------|
| Binaries | ~15–40 MiB | Two static Go binaries |
| State dir | &lt; 5 MiB | keys, applied config, prev binary |
| Journal | operator-controlled | `journalctl` vacuum as needed |

| Load | RAM (rough) | Sessions |
|------|-------------|----------|
| Idle | ~50–120 MiB | 0 |
| Light | ~150–300 MiB | tens |
| Busy | scales with sessions + buffers | set CP `capacity` |

Exact numbers depend on MTU, padding, and concurrent rekeys — measure with `nyxveilctl status` (`memory_bytes`, `sessions`).

## Kernel features

- `tun` module / `/dev/net/tun`
- IPv4 forwarding
- nftables NAT

## What we do not require

- Docker / container runtime
- Local Go toolchain
- Desktop GUI
- Swap (helpful on &lt;1 GiB RAM hosts, not mandatory)
