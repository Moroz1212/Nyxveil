#!/usr/bin/env bash
set -euo pipefail
trap 'echo "SERVER_CONTRACT_GATES=FAIL"' ERR

cd "$(dirname "$0")/.."

run_gate() {
  local name="$1"
  shift
  echo "SERVER_CONTRACT_STEP=${name}"
  "$@"
  echo "SERVER_CONTRACT_STEP=${name}=PASS"
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

# Contract coverage: claim parsing covers expires_at from the server lease.
# Started/progress/result requests remain node-signed; progress refreshes the lease.
run_gate command_ttl_and_lease \
  go test ./internal/controlplane -count=1 -timeout 120s \
  -run 'Test(ClaimNextCommand|MarkCommandStartedProgressAndReportResultSigned)'

run_gate frozen_core_provenance bash scripts/assert-frozen-core.sh

echo 'SERVER_CONTRACT_GATES=PASS'
