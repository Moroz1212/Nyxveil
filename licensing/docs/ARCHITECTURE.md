# Architecture — Nyxveil Control Plane

Version: 1.0.0 · Protocol: **NVP/1** (frozen)

## Role

The Control Plane is the central licensing, catalog, ticket, node registry, and admin system for Nyxveil VPN. It runs on **Windows** (.NET 10, SQL Server, Blazor Server admin UI, HTTPS API, background workers).

## Frozen NVP/1 contracts

Control Plane **must not** change Core wire/auth/failover semantics:

| Rule | Meaning |
|------|---------|
| Location-scoped tickets | Access tickets bind to location scope, not arbitrary global access |
| Same-location failover only | Automatic failover stays within the ticket’s location |
| Refresh never widens | Ticket refresh must not expand locations or node scope |
| Master is a role | `master` is a license/plan role with permissions — **not** a backdoor |
| Device-bound auth | Devices present public keys; private keys never leave the client |
| Signed catalog | Clients trust catalog only with verification keys from Control Plane |

## Logical surfaces

1. **Admin Web** — Blazor Server console (`Nyxveil.ControlPlane.Web`)
2. **Client API** — license validate, device activate, ticket issue/refresh, catalog (`/api/v1/...`)
3. **Node API** — register, heartbeat, config, revocations (`/api/v1/...`)
4. **Workers** — health evaluation, license expiration status, metrics retention, revocation snapshot version

## Solution layout

```
licensing/
  src/
    Nyxveil.ControlPlane.Web/           # Combined host (Windows Service)
    Nyxveil.ControlPlane.Api/           # Controllers + API DI
    Nyxveil.ControlPlane.Application/   # Contracts, options, abstractions
    Nyxveil.ControlPlane.Domain/        # Entities, enums
    Nyxveil.ControlPlane.Infrastructure/# EF Core, Identity, services
    Nyxveil.ControlPlane.Worker/        # Hosted background services
  database/                             # create_database.sql + seed_dev.sql
  scripts/                              # Windows install/update/backup/restore
  docs/
```

## Host composition

`Program.cs` registers:

- `AddApplication` → options + clock
- `AddInfrastructure` → SQL Server, Identity, domain services
- `AddControlPlaneApi` → MVC controllers
- `AddControlPlaneWorkers` → background hosted services
- Cookie auth for Blazor; rate limiting for sensitive API paths (heartbeat excluded)
- Health: `/health/live`, `/health/ready` (DB + signing key)
- Optional SignalR `NodeStatusHub` for dashboard updates

## Data store

Microsoft SQL Server only for production. Schema bootstrap: `database/create_database.sql` (idempotent). EF Core is used by the application; further schema changes should ship as migrations.

## Security posture

- HTTPS fail-closed in Production when `Https:RequireHttpsInProduction` is true
- License raw tokens shown once; only HMAC verifiers stored
- Signing private keys protected via configured key protection path / DPAPI
- Admin RBAC: SuperAdmin / Operator / ReadOnly
