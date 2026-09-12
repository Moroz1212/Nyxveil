# PROJECT.md — Nyxveil verified project map

## Purpose

A compact map of the repository for AI agents.  
For current behavior and commands, always re-check the live code, component docs, and CI workflows.

## Repository

- Repository: `Moroz1212/Nyxveil`
- Default branch: `main`
- Protocol: `NVP/1`
- Frozen Core: `1.0.0`
- Audited snapshot HEAD: `8fc385335a91aa753b879234999f29a2d025abfb`
- Snapshot GitHub branch protection: `main` reported unprotected

The snapshot HEAD is informational. Never reset to it automatically.

## Top-level layout

```text
Nyxveil/
├── .github/workflows/        CI and server release workflows
├── Assets/                   Branding
├── Instructions/             Operational instructions
├── core/                     Frozen NVP/1 Core
├── docs/                     Core/protocol/security/deployment docs
├── server/                   Linux VPN node
├── licensing/                Windows Control Plane / licensing
├── clients/
│   ├── windows/              Windows VPN client
│   └── android/              Android VPN client
├── go.mod                    Root Core module
└── README.md
```

Historical backup/diagnostic artifacts also exist. When live source exists, do not treat a backup ZIP or diagnostic dump as the implementation source of truth.

## Component map

### Frozen protocol Core — `core/`

Root module:

`github.com/nyxveil/nvp`

Snapshot Go version:

`1.24`

Core 1.0.0 is frozen. Current root CI explicitly avoids rewriting it with gofmt.

Key contract docs:

- `docs/CORE-READINESS.md`
- `docs/PROTOCOL.md`
- `docs/TRANSPORTS.md`
- `docs/AUTH-ARCHITECTURE.md`
- `docs/FAILOVER.md`
- `docs/SECURITY-LIMITATIONS.md`

Product code should integrate with the frozen Core, not patch it.

### Linux server — `server/`

Module:

`github.com/nyxveil/server`

Snapshot source version:

`1.1.13`

Snapshot Go version:

`1.24`

Frozen dependency:

```text
replace github.com/nyxveil/nvp => ./third_party/nvp
```

Primary responsibilities:

- Control Plane registration,
- TLS/QUIC listener termination,
- NVP/1 sessions,
- TUN bridge,
- nftables,
- systemd lifecycle,
- status/control,
- configure/update/rollback,
- signed release/update flow.

Primary entry points:

- `server/cmd/nyxveil-server`
- `server/cmd/nyxveilctl`
- `server/installer/install.sh`

Frozen Core provenance/gate:

- `server/THIRD_PARTY_CORE.md`
- `server/scripts/assert-frozen-core.sh`

Snapshot release distinction:

- source candidate: `1.1.14`
- latest published GitHub Release observed: `server-v1.1.13`

### Control Plane / licensing — `licensing/`

Snapshot version:

`1.3.3`

Stack:

- Windows
- .NET 10
- SQL Server
- Blazor
- HTTPS API

Main layout:

```text
licensing/
├── src/Nyxveil.ControlPlane.*
├── tests/
├── database/
├── scripts/
├── docs/
├── appsettings.Example.json
└── VERSION
```

Responsibilities include licensing, devices, tickets, signed node catalog, node registration/heartbeat/config/revocations, admin UI/API, and operational backup/restore/update tooling.

### Windows client — `clients/windows/`

Snapshot version:

`1.1.2`

Main layout:

```text
clients/windows/
├── engine/
├── gui/
├── installer/
├── scripts/
├── docs/
├── third_party/
└── VERSION
```

Go engine module:

`github.com/nyxveil/client-windows`

Snapshot Go version:

`1.25.0`

Core dependency:

```text
replace github.com/nyxveil/nvp => ../third_party/nvp
```

Installed service documented by README:

`NyxveilClientService`

Known drift: README still references 1.0.0 artifacts; `VERSION` is 1.1.2.

### Android client — `clients/android/`

Snapshot version:

`1.0.0`

Main layout:

```text
clients/android/
├── app/
├── bridge/
├── docs/
├── scripts/
├── branding/
├── gradle/
├── build.gradle.kts
├── settings.gradle.kts
└── VERSION
```

Android Go bridge module:

`github.com/nyxveil/client-android/bridge`

Snapshot Go version:

`1.26.0`

Its Go module currently replaces NVP with:

```text
../../../windows/third_party/nvp
```

Android product rule: VPN lifetime belongs to `VpnService`.

## Frozen compatibility contract

Product changes must preserve the frozen NVP/1 behavior unless a Core revision is explicitly authorized:

- TLS 1.3/TCP: no application ALPN.
- QUIC: real HTTP/3 CONNECT, ALPN `h3`.
- Tickets: location-scoped by default.
- Refresh: never widens authorization.
- Automatic failover: same-location only.
- Cross-location: new application session.
- Authentication success: session reaches `ESTABLISHED`.
- MASQUE: not enabled for NVP/1.

## Build/test guidance

Do not use old README snippets blindly. The current CI workflows are the primary command reference.

### Root/Core CI

Read:

`.github/workflows/ci.yml`

Important safety detail: it explicitly does **not** format-write Frozen Core and it handles `core/platform/windows` separately from Linux Core tests.

Never use:

```bash
gofmt -w ./core
```

as a routine command.

### Server CI

Read:

`.github/workflows/server-ci.yml`

It currently includes:

- first-party format check excluding `third_party`,
- `go vet ./...`,
- `go test`,
- installer/update lifecycle tests,
- firewall/manifest/permission/ACME/nftables contract tests,
- selected race tests,
- amd64/arm64 release build/package/verification.

Use:

```bash
cd server
bash scripts/assert-frozen-core.sh
```

when checking Core provenance around server work.

Real clean-host/update/rollback tests can mutate system state; run them only on an authorized disposable Ubuntu 24.04 host.

### Control Plane CI

Read:

`.github/workflows/control-plane-ci.yml`

Current workflow uses:

- .NET 10,
- targeted restore/build,
- unit tests,
- SQL LocalDB integration tests,
- publish,
- local production gate,
- release ZIP packaging,
- extracted-package validation.

Prefer mirroring that workflow over assuming a generic `dotnet test` is the complete gate.

### Windows client

Relevant scripts include:

- `scripts/package-final.ps1`
- `scripts/final-windows-gate.ps1`
- `scripts/assert-frozen-core.ps1`

Packaging can be run in a suitable development environment.  
The elevated final gate can touch Windows services/networking; use only on an authorized test host.

### Android client

Relevant scripts include:

- `scripts/test.ps1`
- `scripts/build-apk.ps1`
- `scripts/install-apk.ps1`

Safe default: test/build.  
Do not install to a connected device unless installation/device testing is authorized.

## Source-of-truth rules by question

Do not use one universal precedence for every kind of fact.

### Runtime behavior

Use:

1. current implementation,
2. tests,
3. current CI/workflow contracts,
4. current component-specific docs.

If they conflict, investigate before changing anything.

### Source version

Use the live component `VERSION` or module/package metadata intended by that component.

### Published release status

Use Git tags/GitHub Releases/release assets.  
A `VERSION` file alone is not proof of publication.

### Readiness

Use current readiness/release documents plus actual test evidence.  
Do not upgrade "local candidate" to "production-ready" because unit/CI tests pass.

### Security/protocol contract

Use frozen Core docs/provenance and implementation.  
Do not weaken a security property merely to satisfy an unrelated test.

## Security/secrets

Never commit or disclose:

- signing private keys,
- license KEKs,
- bootstrap tokens,
- database passwords,
- TLS private keys,
- PFX passwords,
- DPAPI sensitive exports,
- user/device secrets.

Do not weaken auth/TLS/update/provenance checks for convenience.

Do not advertise guaranteed undetectability, DPI-proof operation, censorship-proof operation, or perfect security.

## Multi-agent workflow

One worktree should have one writer at a time.

Serial handoff:

1. agent reads `AGENTS.md`, `AI_STATE.md`, latest `AI_CHANGELOG.md`, `PROJECT.md`;
2. agent checks live HEAD/worktree;
3. agent works only on requested scope;
4. agent runs safe relevant tests;
5. agent reviews its diff;
6. agent updates state/changelog;
7. next agent resumes from repository state, not from another account's chat history.

Parallel work must use separate branches/worktrees.

Because `main` was unprotected at the audited snapshot, do not rely on GitHub branch protection as a safety net. Prefer a task branch for AI work and do not push `main` without explicit user authorization.

## Release/external-action boundary

Code preparation is not release authorization.

Without an explicit user request, do not:

- push,
- tag,
- publish a release,
- deploy,
- mutate production services/database/network/DNS,
- install drivers/services on the user's primary system,
- rotate production secrets/certificates.

This boundary prevents a new AI account/session from interpreting project state as permission for external operations.
