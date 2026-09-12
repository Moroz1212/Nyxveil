#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

run_gate() {
  local name="$1"
  shift
  echo "SERVER_OPERATOR_STEP=${name}"
  "$@"
  echo "SERVER_OPERATOR_STEP=${name}=PASS"
}

run_gate durable_update_recovery \
  go test ./internal/runtime -count=1 -timeout 120s \
  -run 'Test(Legacy119MarkerDeleted|CorrelatedUpdate|JournalWithoutCommandID|TargetVersionMismatch|NodeIdentityMismatch|AmbiguousCorrelatedJournals|IntactMarkerPath|CorrelatedRollback|CompletePendingUpdateReportsProgress)'

run_gate durable_update_rollback \
  go test ./internal/updater -count=1 -timeout 120s \
  -run 'Test(ApplySHAAndRollback|UpdaterRollback|RollbackRestores|ManagementAssetRollback|AuxiliaryFilesRollback)'

run_gate acme_state_migration \
  go test ./internal/filemeta -count=1 -timeout 120s \
  -run 'Test(MigrateACMEState|ValidateRuntimeACME)'

# Claim parsing covers expires_at from the server lease. Started/progress/result
# requests must remain node-signed; progress is the lease-refresh contract.
run_gate command_ttl_and_lease \
  go test ./internal/controlplane -count=1 -timeout 120s \
  -run 'Test(ClaimNextCommand|MarkCommandStartedProgressAndReportResultSigned)'

run_gate frozen_core_provenance bash scripts/assert-frozen-core.sh

echo 'SERVER_OPERATOR_E2E=PASS'
