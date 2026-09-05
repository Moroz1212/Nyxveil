# Admin UI

Blazor Server console hosted by `Nyxveil.ControlPlane.Web`.

## Roles

| Role | Capabilities |
|------|----------------|
| **SuperAdmin** | Everything, including Signing Keys and Admin Users |
| **Operator** | Operational management (licenses, nodes, bootstrap, etc.) — **not** signing keys or admin account management |
| **ReadOnly** | View only |

## First SuperAdmin

1. Apply database schema
2. Configure `Security:LicenseKekHex` and connection string
3. Start the host
4. Open `/setup` when no SuperAdmin exists
5. Create the first admin (password never echoed in scripts)

Alternatively, create via CLI:

```text
Nyxveil.ControlPlane.Web.exe admin create --username admin@example.com
```

Password on stdin (installer pipes SecureString). Exit `2` if SuperAdmin already exists. Production disables anonymous `/setup` (`Setup:AllowWebBootstrap=false`).


## Pages

- Dashboard — live operational counters
- Licenses — create (one-time raw key), extend, revoke; search/filter
- Users / Devices / Plans / Locations
- Nodes / Node details — enable, disable, drain, test-only, maintenance (confirm dialogs)
- Metrics, Revocations, Bootstrap Tokens
- Signing Keys (SuperAdmin)
- Admin Users (SuperAdmin)
- System Settings, Audit Log
- Account login / logout / access denied

## Auth

ASP.NET Core Identity with cookie authentication. Secure cookies, antiforgery enabled for Blazor.
