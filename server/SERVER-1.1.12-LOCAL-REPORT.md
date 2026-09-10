# Server 1.1.12 — local continuation report, 2026-09-10

FINAL VERDICT: NOT READY.

Continuation base HEAD (local and GitHub main verified): 9b7321b4b3b3d686118e689775f998deed707c9d. The capability-assertion fix is recorded by the commit containing this report; its exact SHA and new CI evidence will be recorded after CI completes. Server remains 1.1.12. Toolchain: Go 1.25.0 Windows/amd64; CI remains Go 1.24, Ubuntu 24.04. The artifacts below are historical local candidates built from 20eaedc8faef0a5a535503c882a34ec9d6e51dbc with uncommitted changes (Commit=20eaedc-dirty), not artifacts of the continuation HEAD.

## Current continuation

- Root cause: test-bounded-runuser-timeout.sh still expected ambient-caps=+net_bind_service, although the production fallback already restricts capabilities with -all,+net_bind_service.
- The test independently requires --bounding-set=-all,+net_bind_service, --no-new-privs, --inh-caps=-all,+net_bind_service and --ambient-caps=-all,+net_bind_service, plus the setpriv user switch. No production capability contract was weakened.
- PASS: targeted test-bounded-runuser-timeout.sh executed on Ubuntu/WSL, including all four assertions, stdin, exit status, timeout and TERM delivery.
- PASS: fresh go test -timeout 120s ./... and go vet ./... on Windows; test-installed-modes.sh on Ubuntu/WSL; assert-frozen-core.sh and git diff --check. No server/third_party or licensing changes against base HEAD.
- Windows mock executable-assets are already fixed in base HEAD: assert_installed_mode bypasses Unix mode checks only for Windows MOCK, while Linux mocks and production retain strict checks. test-installed-modes.sh covers this boundary.
- Previous Server CI 34471564225 on 9b7321b4b3b3d686118e689775f998deed707c9d: FAIL only at the stale capability assertion, with exact error `FAIL missing setpriv ambient-caps fallback`. All preceding steps passed, including Linux management-assets (`test-installer-management-assets PASSED`) and platform/firewall contracts. Remaining Linux stages and build were SKIP, not PASS.
- New CI, its artifact ID, complete artifact verification and disposable live gates: pending. No GitHub Release or production deployment authorized in this continuation.
- Follow-up HEAD da2cdd80ebed64cc92e7f0c554c60fdd0028f13e, CI 34498579036: capability wrapper, real ACME privileged bind, exact setpriv capability sets, nftables/private netns, bounded HTTP and clean-host contract all PASS. Race stage FAIL: the ACME fixture left the nyxveil account behind, so later Go tests running as runner attempted forbidden chown on temporary TLS files. No data-race diagnostic was reported; runtime/updater failed ownership setup and build was SKIP.
- CI fixture correction: server-ci.yml now removes only the service account created by its ACME step using an EXIT trap and bounded userdel. Pre-existing accounts are preserved; cleanup failure fails the step. Production file ownership enforcement is unchanged. A fresh CI run must execute race, build, package and artifact gates.
- Follow-up HEAD 12e16e8b7971b34d072122700d13e0ab20100ae1, CI 34498962307: all Linux tests including race PASS; amd64/arm64 cross-compile PASS. Package reached successful manifest/hash checks, then FAIL in verify-live-final-consumer.go because its loopback HTTP fixture lacked NYXVEIL_TEST_MODE=1. The harness now sets this flag only for its --verify-chain subprocess. Production pinned GitHub origin enforcement remains unchanged; verify-release/upload still require a new successful run.

## Root causes and behavior changes

- Registration: the setpriv fallback retained unrelated capability sets, while systemd-run and configure registration lacked complete outer time bounds. Both paths now restrict capability sets to CAP_NET_BIND_SERVICE, set NoNewPrivileges, and bound execution. Transient units have runtime/stop limits and bounded cleanup; no permanent setcap or global sysctl change is introduced.
- Firewall: separate deletion before loading and unit stop/start created a gap where a failed replacement could leave no Nyxveil table. The installer, configure path and unit now use the atomic destroy+load file without a preceding deletion. Configure validates the proposed rules before replacing its file, bounds external commands, and reports systemctl failures. Installer no longer ignores firewall activation failure. Explicitly stopping the firewall unit now retains its rules; removal is an uninstall concern.
- Gate evidence: skipped/non-root OS checks previously printed PASS, and the nft test mutated the host table. OS omissions now report SKIP; full bootstrap secret hygiene remains PARTIAL. The nft test runs in a private network namespace and checks the sample plus actual installer writer across six applies, foreign-table preservation, and a failed transaction. Added Linux regression tests for invalid rules preserving the previous configuration and propagation of unit failures.
- Release: explicit CI run IDs were not bound to the source commit/workflow; existing assets could be clobbered; tag creation could silently fall back; artifact verification required absent build staging and executable modes lost by ZIP transport. Release workflow now validates successful push provenance, repository, workflow path, commit and tag; requires one unexpired artifact and verifies its ZIP digest before extraction; restores script permissions; verifies artifact content without requiring build staging; and treats an identical existing release as a no-op. Different asset sets or bytes fail closed. No binaries are rebuilt by the release workflow.

## Files changed

.github/workflows/server-ci.yml
.github/workflows/server-release.yml
server/installer/install.sh
server/internal/configure/firewall_linux.go
server/internal/configure/firewall_linux_test.go
server/internal/configure/transaction.go
server/systemd/nyxveil-firewall.service
server/scripts/_run-shell-ci.sh
server/scripts/assert-existing-release-matches-dist.sh
server/scripts/assert-release-bytes-identity.sh
server/scripts/clean-host-install-gate.sh
server/scripts/test-acme-privileged-bind.sh
server/scripts/test-nftables-idempotency.sh
server/scripts/verify-release.sh

## Security and compatibility

No Core, wire protocol, NodeAuth, client or Control Plane changes. No version bump. Node identity and registration contracts retained. Firewall service stop behavior intentionally retains rules. New registration limits and release provenance checks fail closed. Existing pre-1.1.12 packages without the destroy preamble are not evidence for the new firewall behavior.

## Executed validation

PASS — go test -timeout 120s ./... on Windows (platform-specific tests may skip).
PASS — go vet ./... on Windows.
PASS — go build ./... for Linux amd64 and arm64; configure test binaries cross-compiled for both targets.
PASS — installer syntax/structure checks.
PASS — post-registration identity mock tests: ambiguous registration preserves key/config; staging removed; repair requires bootstrap and retains identity.
PASS — manifest shell/Go interoperability, including existing server-v1.0.0 manifests.
PASS — installer curl/mock paths, version resolution, pinned installer, remote-update and certificate gate contracts, clean-host gate contract (shell suite logs).
PASS — bounded HTTP fault tests: stalled headers/body, reset, 404/500, partial download and connection hang; no final/partial residue.
PASS — local packaging, verify-release in build and --ci-artifact modes, upload-set completeness, manifest hashes, SHA256SUMS, pinned installer, LOCAL_ARTIFACT_INTEGRITY.
PASS — git diff --check.

RESOLVED — the earlier Windows mock executable-assets failure is fixed in 9b7321b; Linux management-assets passed in CI 34471564225. Initial local missing Python/jq failures were environment-related; bounded HTTP and manifest interoperability passed after providing real tools.
SKIP — ACME_PRIVILEGED_BIND and NFTABLES_IDEMPOTENCY runtime checks on Windows; static/mock wrapper checks passed.
PARTIAL — BOOTSTRAP_SECRET_HYGIENE: mock stdin/argv checks only, not full process/log/filesystem observation of production registration.
NOT EXECUTED — Linux race tests; execution of new Linux firewall tests; real transient-capability bind test; real nft transactions; real install/repair x3; real ACME failure; clean-host, reboot, live update/rollback and served-SPKI gates.
NOT EXECUTED — actual release bytes identity between a successful CI artifact and GitHub Release. Local integrity is not that proof.
FAIL — GitHub Actions Server CI 34471564225, verified against base HEAD, stopped at the stale capability assertion described above. A new run is required after committing this correction.

## Local artifacts

Directory: server/dist/release

nyxveil-server-1.1.12-linux-amd64.tar.gz
Size: 8237613 bytes
SHA256: fb1fe5e7081bc5ec7ff1459d520c25123c9fb22039893d9cbc18d0ce71e65654

nyxveil-server-1.1.12-linux-arm64.tar.gz
Size: 7473658 bytes
SHA256: cc9e81df70861a665b20c17c4d48605c14c4272741b7ec6c1ea934e725c59b99

Flat upload set contains 18 entries in UPLOAD-LIST-server-v1.1.12.txt: three binaries for each architecture; production-gate.sh; install.sh; nyxveil-update.service; 50-nyxveil-management.rules; VERSION; THIRD_PARTY_CORE.md; two manifests; bootstrap-cli-update.sh; live-final-update.sh; SHA256SUMS; upload list. Per-file hashes are in SHA256SUMS. Archive hashes above were calculated from actual final local files.

## Frozen Core

Before/after expected baseline: 7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b.
PASS — no diff in server/third_party against starting HEAD; no licensing changes.
The existing assert-frozen-core script passed its available checks. The authoritative frozen ZIP was not present in the file inventory, so independent recomputation of that ZIP hash is NOT EXECUTED; do not interpret its printed expected hash as new archive evidence.

GITHUB PUSH: base HEAD is on main; continuation correction pending push.
GITHUB RELEASE: NO
PRODUCTION DEPLOY: NO

Remaining required evidence: green Linux CI on the final committed source, exact CI artifact identity, and the disposable clean-host/live gates. This report does not establish readiness for release or production.

API contract consulted for artifact IDs/digests/downloads: https://docs.github.com/en/rest/actions/artifacts
