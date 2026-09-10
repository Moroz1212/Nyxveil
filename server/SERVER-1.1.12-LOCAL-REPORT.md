# Server 1.1.12 — local continuation report, 2026-09-10

FINAL VERDICT: NOT READY.

Source HEAD: 20eaedc8faef0a5a535503c882a34ec9d6e51dbc, plus uncommitted changes listed below. Local binaries identify Commit=20eaedc-dirty. Toolchain: Go 1.25.0 Windows/amd64; CI remains Go 1.24, Ubuntu 24.04. These are local candidate artifacts, not verified CI artifacts or published releases.

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

FAIL — complete local shell suite is not green: test-installer-management-assets cannot establish Unix executable semantics for its plain-text mock asset on Git Bash. An experimental fixture adjustment was reverted. Initial missing Python/jq failures were environment-related; bounded HTTP and manifest interoperability passed after providing real tools. The management-assets test still needs Linux execution.
SKIP — ACME_PRIVILEGED_BIND and NFTABLES_IDEMPOTENCY runtime checks on Windows; static/mock wrapper checks passed.
PARTIAL — BOOTSTRAP_SECRET_HYGIENE: mock stdin/argv checks only, not full process/log/filesystem observation of production registration.
NOT EXECUTED — Linux race tests; execution of new Linux firewall tests; real transient-capability bind test; real nft transactions; real install/repair x3; real ACME failure; clean-host, reboot, live update/rollback and served-SPKI gates.
NOT EXECUTED — actual release bytes identity between a successful CI artifact and GitHub Release. Local integrity is not that proof.
NOT EXECUTED — GitHub Actions run for these uncommitted changes. gh is not authenticated in this environment; no workflow run ID or artifact ID is claimed.

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

GITHUB PUSH: NO
GITHUB RELEASE: NO
PRODUCTION DEPLOY: NO

Remaining required evidence: green Linux CI on the final committed source, exact CI artifact identity, and the disposable clean-host/live gates. This report does not establish readiness for release or production.

API contract consulted for artifact IDs/digests/downloads: https://docs.github.com/en/rest/actions/artifacts
