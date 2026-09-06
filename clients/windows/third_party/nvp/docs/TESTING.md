# Testing — NVP/1

## Unit and Integration

From repository root (Go **1.24+**):

```bash
go test ./core/...
go test ./core/integration/ -count=1
go test ./core/integration/ ./core/connector/ ./core/session/ -count=1
go test -race ./core/session/ ./core/packet/ ./core/auth/ticket/
```

Notable packages:

| Package | Coverage focus |
|---------|----------------|
| `core/packet` | Wire encode/decode, padding flags, fuzz |
| `core/session` | State machine, rekey, handshake, `WaitEstablished` cleanup |
| `core/auth/ticket` | JWT issue/verify, session binding |
| `core/controlplane/...` | Catalog sign/verify, CP stub/production, refresh entitlements |
| `core/transport/ech` | ECH policy unit tests |
| `core/connector` | Production connector SPKI / failover / AUTH→ESTABLISHED |
| `core/failover` | Selection + `ExhaustedError` tried nodes |
| `core/integration` | End-to-end session, **real TLS/QUIC sockets**, MITM, failover, chaos, fingerprint |

## Real loopback (sockets, not memory)

| Test | Asserts |
|------|---------|
| `TestRealTLSFullSession` | TLS handshake → AUTH → AUTH_OK → ESTABLISHED → DATA C→S/S→C → PING/PONG → close |
| `TestRealQUICFullSession` | Same over HTTP/3 CONNECT |
| `TestRealTLSRekeyAndPostRekeyData` | Forced rekey (`RekeyPacketCount=2`) + post-rekey DATA |
| `TestRealQUICRekeyAndPostRekeyData` | Same on QUIC |
| `TestNoGoroutineLeakAfterSessionClose` | `runtime.NumGoroutine` before/after GC; bounded delta |

Helpers: `testutil.CertBundle`, `authhandler.AuthHandler`, ticket issuer (`setupTicket`).

## P0 Regression Tests (post-fix)

| Test | Package | Asserts |
|------|---------|---------|
| `TestTicketRefreshWrongDeviceRejected` | `controlplane/server` | Refresh with mismatched device → rejected |
| `TestTicketRefreshRevokedLicenseRejected` | `controlplane/server` | Refresh after license revoke → rejected |
| `TestRefreshUsesCurrentLicenseRole` | `controlplane/server` | Refresh role from current plan |
| `TestRefreshAfterLicenseDowngradeDoesNotKeepOldRights` | `controlplane/server` | No stale entitlements |
| `TestUserDoesNotReceiveTestNodes` | `controlplane/server` | User catalog omits `test_only` |
| `TestMasterCanReceiveTestNodes` | `controlplane/server` | Master catalog includes `test_only` |
| `TestProductionConnectorAcceptsCorrectSPKI` | `connector` | Correct pin accepted |
| `TestProductionConnectorRejectsWrongSPKI` | `connector` | Wrong pin rejected |
| `TestOpenSessionWaitsForAuthOK` | `connector` | Success only when ESTABLISHED |
| `TestExhaustedErrorWrapsTransportUnavailableAndListsTriedNodes` | `failover` | Typed aggregate error |
| `TestSPKIPinPersistsAcrossRestart` | `controlplane/store` | SPKI survives reopen |
| `TestHeartbeatDoesNotOverwriteNodeConfiguration` | `controlplane/store` | `UpdateNodeHealth` ≠ config overwrite |
| `TestMigrationOldSchemaToNew` | `controlplane/store` | Old DB columns migrate (SPKI etc.) |
| `TestECHRequiredSuccess` | `transport/ech` | Required + negotiated OK |
| `TestECHPreferredFallback` | `transport/ech` | Preferred allows non-ECH |
| `TestECHServerKeyRotation` | `transport/ech` | KeySet rotation helper |

## Fingerprint Regressions

`core/integration/fingerprint_test.go` asserts:

1. No plaintext magic (`NVP1`, `NYX`, …) on the wire before the secure channel
2. Production dial: TLS has empty NextProtos; QUIC uses real `h3` + CONNECT (no custom ALPN / no `nvp/1`)
3. Enabled padding increases ciphertext size variance vs padding-off

## Local Transport Loopback

```bash
go run ./core/cmd/nvp-test-server/ -listen 127.0.0.1:4433 -write-ca /tmp/nvp-ca.pem
go run ./core/cmd/nvp-test-client/ -addr 127.0.0.1:4433 -ca /tmp/nvp-ca.pem -transport auto
```

## Fuzz Smoke

CI (`go-version: '1.24'`) and locally:

```bash
go test ./core/packet/ -fuzz=FuzzDecodeWireRecord -fuzztime=3s
go test ./core/packet/ -fuzz=FuzzDecodeInner -fuzztime=3s
go test ./core/auth/ticket/ -fuzz=FuzzTicketVerify -fuzztime=3s
go test ./core/controlplane/catalog/ -fuzz=FuzzParseCatalog -fuzztime=3s
```

## What Is Not Covered

- Real TSPU / operator DPI stands
- Production MASQUE (stub `Available()=false`)
- Independent external security audit

See [CORE-READINESS.md](CORE-READINESS.md). Independent security/cryptographic audit: **NOT PERFORMED**
