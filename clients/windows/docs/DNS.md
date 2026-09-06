# DNS policy (Windows Client 1.0.0)

## Source

DNS servers come **only** from node TypeConfig `dns_servers` (server ≥ 1.0.2),
which the operator configures in `server.json`. Empty → connection FAIL.

No hardcoded 8.8.8.8 / 1.1.1.1. No auto-fill from TUN gateway.

## Apply order

After AUTH_OK + TypeConfig validation → configure Wintun → set DNS on the TUN
interface → then default IPv4 route.

## Disconnect / crash

Restore previous Windows DNS configuration exactly.

## Leak status

Setting DNS on Wintun alone is **not** claimed as hardened zero-leak protection
until dedicated leak tests pass. Diagnostics will surface configured DNS.
