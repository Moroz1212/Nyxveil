# Firewall (nftables)

Nyxveil uses an **isolated** nftables table: `inet nyxveil`.

## Hard rule

**Never** run `nft flush ruleset` on a production host. The installer and sample config only create/replace table `inet nyxveil`.

## Sample

See `firewall/nftables-nyxveil.conf`:

- **input** — accept new TCP TLS + UDP QUIC on configured ports
- **forward** — allow traffic to/from `nyxveil0`
- **postrouting** — masquerade `vpn_subnet` egress

Installer writes `/etc/nftables.d/nyxveil.conf` and applies with `nft -f` after `nft delete table inet nyxveil` (best-effort) so only our table is replaced.

Persistence is via **`nyxveil-firewall.service`** (Type=oneshot, RemainAfterExit=yes):

- **Start:** `nft -f /etc/nftables.d/nyxveil.conf`
- **Stop:** `nft delete table inet nyxveil`
- **Ordering:** `Before=nyxveil-server.service`; the server unit `Wants=` / `After=` the firewall unit

So a reboot re-applies the isolated table before the node starts.

## Coexistence

Host rules in other tables (`inet filter`, `ip nat`, ufw-managed sets, etc.) remain untouched. If a higher-priority drop policy blocks 443, adjust the **host** policy or raise Nyxveil chain priority carefully.

## Verify

```bash
nft list table inet nyxveil
```

## Uninstall

`installer/uninstall.sh` deletes table `inet nyxveil` only.
