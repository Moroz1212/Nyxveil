# Nyxveil Control Plane 1.2.0

## Highlights

- Web UI **Инфраструктура** (`/admin/infrastructure`): CP + node certificate health, remote ops, recent commands.
- Node remote commands (NodeAuth queue): RenewCertificate, RestartNyxveilService, RebootHost — no SSH.
- Control Plane DNS-01 certificate renewal wizard (manual TXT at registrar) with durable state (no private keys in DB).
- Certes ACME DNS-01 + reuse of existing `tls configure` / Windows Store / ACL / rollback.
- Heartbeat optional fields: `management_capabilities`, `boot_id`, `supports_commands`.
- Schema version **3** (`003_node_commands_cert_renewal.sql`); v2 validators retained.
- Companion node agent: server application **1.1.8** (NVP/1 unchanged).

## Compatibility

- NVP/1, Frozen Core, license/catalog/ticket semantics, and existing NodeAuth signing are unchanged.
- Migration is additive only (no DROP/rename of existing columns).
- Nodes without command support keep heartbeat/VPN; UI shows remote management unsupported.
