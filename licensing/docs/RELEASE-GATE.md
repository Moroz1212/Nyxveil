# Control Plane 1.0.0 release gate

Release: Nyxveil-ControlPlane-v1.0.0-LIVE-DEPLOY-FROZEN.zip.
Target: first Windows + Microsoft SQL Server deployment.

## Final changes

- Normal node requests require Ed25519 nvp-node-req-v2 bound to NodeId, timestamp,
  nonce, actual method, canonical path/query and SHA-256 of the raw body.
- Resource authorization checks every supplied route/query/body identity. Node A
  cannot mutate Node B. Request buffering is bounded and runs before model binding.
- MSSQL composite nonce PK atomically arbitrates replay; expiry cleanup preserves
  the whole validity window. Same-second heartbeat/config uses independent nonces.
- Frozen nvp-node-v1 is confined to existing-node registration PoP. Registration
  cannot replace existing identity/key or issue a new credential on retry.
- Both config pulls and registration return canonical location_id. ConfigVersion
  is a DB optimistic concurrency token. Config, Node projection and audit commit
  together or roll back. Conflicts require reload/retry.
- Zero admin capacity is respected by heartbeat and catalog. Disabled locations
  are excluded even for unrestricted callers.
- Refresh intersects old/current permissions and cannot elevate a role. Catalog
  ticket access rechecks current license/device/revocation state. Revocation
  snapshots retain all entries instead of truncating older revocations.
- SQL bootstrap DDL is generated from InitialCreate and exactly compared in tests.
  Installer SQL copies bind the selected database; sqlcmd selects the intended
  database for queries/migrations and connects to master for fresh creation.
  Live SQL test-harness literal quoting was corrected.

## Local checks

216 passing .NET tests: 177 unit, 39 HTTP/relational integration; no failures/skips.
Release restore/build/test/publish, format verification and PowerShell checks pass.
Schema model/snapshot/migration/bootstrap alignment passes. NuGet vulnerability
scan includes transitive packages and the interop project: High 0, Critical 0,
NU1904 0. PSScriptAnalyzer was not installed.

Production TicketService -> Frozen Go VerifyAt: PASS.
Production CatalogService -> Frozen Go Parse/Verify: PASS.
Frozen Go NodeToken -> C# compatibility verifier: PASS.
Production managed catalogs -> Frozen selector (maintenance/draining/disabled): PASS.
Independent Go req-v2 signatures -> C# production verifier: PASS.

Authoritative Frozen archive SHA256 before/after:
7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b

Node API tests cover cross-node authorization, actual method/path/query/body
binding, expiry/future timestamps, replay and concurrent replay, immediate config
pulls, location propagation, admin concurrency and database audit-write rollback.
Existing tests retain ticket scope/audience, device key binding, role/TestOnly,
Windows deployment, TLS, DPAPI and unified Signing Keys + License KEK recovery.

## Live validation still required

LIVE MSSQL DDL: NOT VERIFIED. No SQL Server instance was available. Relational
transaction/replay tests run on SQLite; run the SQL Server DDL/concurrency checks
on the deployment VM. SQLite is a test dependency, not a production provider.

LIVE WINDOWS SERVICE: NOT VERIFIED. The build host is Windows, but no installation,
certificate/SQL provisioning or service start was performed on it. Perform real
install/self-test/recovery on the isolated deployment VM before proceeding to server/.

The accompanying external release report records the final ZIP hash, entry count,
CRC/path portability checks and the gate results from a fresh ZIP extraction.
