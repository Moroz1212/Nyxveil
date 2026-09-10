# Server 1.1.12 — continuation report, 2026-09-10

FINAL VERDICT: READY FOR DISPOSABLE CLEAN-HOST LIVE GATE.

Verified implementation HEAD: 308248ed8be879637bd480b2a1777e1843d6faca (main).
Starting HEAD: 9b7321b4b3b3d686118e689775f998deed707c9d, independently matched to GitHub main before editing.
Server VERSION: 1.1.12. Local Go: 1.25.0 Windows/amd64. CI: Go 1.24, Ubuntu 24.04.
The commit containing this final report changes documentation only. The implementation evidence below is pinned to the exact source/run/artifact stated here; a successor CI artifact must be checked independently, never assigned these hashes. Final task output records the terminal HEAD/run/artifact, with full local evidence under server/dist/ci-evidence/<run-id>/.

## Root causes and corrections

1. CI 34471564225 on starting HEAD failed only at the stale `ambient-caps=+net_bind_service` assertion. Linux management-assets and all preceding steps passed; subsequent stages were skipped. The test now independently requires bounding-set=-all,+net_bind_service, no-new-privs, inh-caps=-all,+net_bind_service and ambient-caps=-all,+net_bind_service. Production implementation was already strict and is unchanged.
2. CI 34498579036 on da2cdd80ebed64cc92e7f0c554c60fdd0028f13e passed capability, ACME, nftables, bounded HTTP and clean-host contract, then failed race-stage fixture setup: ACME left a nyxveil account behind, and later tests as runner could not chown temporary TLS files to it. The ACME step now cleans up only the account it creates, using an EXIT trap and bounded userdel; cleanup errors fail the step. Pre-existing accounts are preserved. Production ownership enforcement is unchanged.
3. CI 34498962307 on 12e16e8b7971b34d072122700d13e0ab20100ae1 passed all Linux tests/race and both builds, then package consumer verification failed because its loopback HTTP fixture omitted NYXVEIL_TEST_MODE=1. The Go verifier now sets that flag only on its --verify-chain subprocess. Production pinned GitHub origin restrictions remain unchanged.

Windows mock executable-assets were already fixed in starting HEAD: the Unix-mode bypass applies only to Windows MOCK; Linux mocks and production remain strict. This is covered by test-installed-modes.sh and the Linux management-assets CI stage.

## Files changed in this continuation

- server/scripts/test-bounded-runuser-timeout.sh
- .github/workflows/server-ci.yml
- server/scripts/verify-live-final-consumer.go
- server/SERVER-1.1.12-LOCAL-REPORT.md

No production installer, runtime, updater, protocol, NodeAuth, clients, Control Plane or VERSION changes. No migration or compatibility change. Capability, ownership and release-origin security contracts were preserved.

## Executed validation

PASS — targeted bounded wrapper test on Ubuntu/WSL: four capability assertions, stdin, exit status, timeout and TERM.
PASS — negative mutation checks: removing each of the four flags from a temporary installer copy independently produces the expected FAIL.
PASS — go test -timeout 120s ./... and go vet ./... on Windows; go vet of the changed standalone Go verifier; format and git diff --check.
PASS — test-installed-modes.sh on Ubuntu/WSL; test-release-origin.sh rejects non-production origin without test mode.
PASS — Server CI 34499467541, both test and build jobs, on verified implementation HEAD. Every required stage ran successfully: management-assets; bounded wrapper; real ACME privileged :80 bind through systemd and setpriv; exact capability masks; nftables repeated atomic applies/private netns; bounded HTTP; clean-host contract; Linux race; cross-compile amd64/arm64; package; verify-release; complete upload list; artifact integrity; artifact upload.
PASS — complete CI ZIP downloaded; GitHub artifact ID/run/head/push/main/workflow provenance and ZIP digest/size checked against API and upload log.
PASS — all 165 extracted files inventoried and hashed; 18/18 upload entries present; 16/16 SHA256SUMS entries match; both manifests match actual CI bytes, required assets, destinations, modes, version, architecture and Core/protocol requirements.
PASS — both tar.gz archives match all extracted directory entries and bytes (78 entries per architecture), with root ownership and contract executable modes. All six ELF binaries have the expected architecture and Go VCS metadata matching 308248ed8be879637bd480b2a1777e1843d6faca, vcs.modified=false.
PASS — downloaded artifact independently checked with verify-release.sh --ci-artifact, verify-artifact-set.sh, assert-release-bytes-identity.sh and Go manifest parsing. The real shell consumer --verify-chain ran on Ubuntu/WSL against the downloaded artifact and passed.

PARTIAL — bootstrap secret hygiene: mock stdin/argv checks; full live registration observation remains required.
NOT EXECUTED — disposable clean-host install/repair, live ACME issuance/failure, live update/rollback, reboot and served-SPKI gates. CI privileged-bind and nft fixtures are not live E2E.
NOT EXECUTED — comparison against published GitHub Release bytes; no release was created.

## Verified CI artifacts (not historical local builds)

Run: 34499467541
Artifact ID: 10161312534
Artifact filename: nyxveil-server-binaries.zip
Size: 48224606 bytes
SHA256: eb43393bb9635217c653278293e1ca09857aa18714498a0aebf6d24706f581f7

nyxveil-server-1.1.12-linux-amd64.tar.gz
Size: 8415120 bytes
SHA256: 95058083f2fa109dccc4b559842a3c159c7221ec1b5a122fa07e061c12e8ffae

nyxveil-server-1.1.12-linux-arm64.tar.gz
Size: 7747874 bytes
SHA256: 7429105c66f70fe3390c57c98331535c078cde80ead735ed52174918bfc51abd

Evidence: server/dist/ci-evidence/34499467541/ contains run.json, jobs.json, artifact.json, test.log, build.log, the downloaded ZIP and artifact-verification.json with every extracted filename, size and SHA256. Prior local dist/release artifacts were preserved separately and are not the CI evidence above.

## Frozen Core and release status

PASS — assert-frozen-core.sh locally and in CI. Expected frozen ZIP SHA before/after: 7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b.
PASS — vendored Core tree before/after unchanged: e48c8ba41c4bce35ad2f63afddab4a3da3765e9a. Control Plane/licensing tree unchanged: 088e2e3bc04f61fc9540d4ba539ac45a692ed839.
The authoritative frozen ZIP is absent; independent recomputation of that ZIP hash is NOT EXECUTED. Tree identity and the available provenance gate establish no Core changes in this continuation.

GITHUB PUSH: YES, main.
GITHUB RELEASE: NO.
PRODUCTION DEPLOY: NO.

Residual gate: execute a disposable clean-host live install/repair with the independently verified final CI artifact before release or production. This report does not claim readiness for release or production rollout.
