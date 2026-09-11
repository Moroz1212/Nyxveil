# Server 1.1.14: legacy update terminal recovery

Local candidate. No GitHub tag, release, or production deployment is authorized by this change.

## LIVE root cause (1.1.9 → 1.1.13)

Remote update from Server **1.1.9** to **1.1.13** installed and verified successfully
(`update_success=true`, installed production gate `RESULT=PASS`, no rollback). The new
daemon stayed on 1.1.13 and connected to Control Plane, but automatic terminal result
`updated_healthy` never arrived.

Proven sequence:

1. Control Plane drained the node and dispatched `UpdateNodeLatest`.
2. Legacy 1.1.9 wrote `update-command.json` (only durable store of CP `command_id`) and
   started `nyxveil-update.service`.
3. New ctl completed self-handoff / post-check / gate and retained a `phase=committed`
   transaction journal **without** `command_id`.
4. Restart terminated the 1.1.9 parent; its in-process fallback attempted
   `ReportCommandResult` with a canceled context (`context canceled`) and then
   **unconditionally deleted** `update-command.json`.
5. Server 1.1.13 `completePendingUpdate` returned immediately when the marker was
   missing, so the surviving committed journal could not finish the CP command.
6. CP eventually expired the command as `expired_outcome_unknown`; SuperAdmin
   reconciliation later confirmed `updated_healthy` from evidence.

## Fix

1. **Capture before legacy race** — `nyxveilctl update-resume` (1.1.14) reads the still-
   present `update-command.json` and durably writes `command_id` (+ started_at /
   previous_version when available) into the transaction **before** any restart that
   can kill a 1.1.9 parent. Target/previous mismatches fail closed.
2. **Additive journal fields** — `command_id`, `command_started_at`, `result_queued_at`,
   `result_reported_at` are optional; historical journals without them still load.
3. **Marker-missing recovery** — daemon recovers only from terminal journals that carry
   an exact non-empty `command_id`, matching node identity when present, and matching
   version evidence (`committed` ⇒ installed==target; rollback healthy ⇒ installed==previous).
4. **Fail closed** — journals without `command_id`, target mismatch, node mismatch, or
   multiple ambiguous correlated journals never invent a CP result.
5. **Exactly-once delivery** — result is first written to the durable pending-result
   store; journal is marked queued then consumed only after CP acknowledgement (or when
   pending is gone). Replay after consume is a no-op.
6. **Ownership** — transaction files/dirs are re-owned to the `nyxveil` service account
   after each atomic write so a root-run update unit leaves journals readable by the
   daemon.

Lifecycle-aware Drain/Maintenance semantics from 1.1.13 are unchanged. The updater still
does not undrain; CP restores `admin_state_before` after `updated_healthy`.

## Failure modes

| Condition | Behavior |
|-----------|----------|
| Marker present, target mismatch | update-resume fails closed before restart |
| Marker absent (manual update) | proceed without `command_id` |
| Committed journal, no `command_id` | ignored (operator reconciliation remains) |
| Multiple terminal journals for same `command_id` | refuse; log ambiguity |
| CP temporarily down after commit | pending result + `result_queued_at` retained; retry |
| Crash after queue before POST | pending store + journal survive; retry on start |
| Already reported | journal consumed; no duplicate invent |

## Compatibility

- Core **1.0.0** / NVP/1 unchanged.
- Frozen Core hash unchanged.
- Control Plane API unchanged; CP 1.3.3 SuperAdmin reconciliation remains emergency fallback.
- Actual Server **1.1.9** artifact behavior is the compatibility target for the next LIVE gate.

## Required LIVE validation (not claimed by this local work)

Disposable Ubuntu 24.04 + systemd + TUN + nftables + real CP:

CP Drain → sessions=0 → real Server 1.1.9 starts update → 1.1.14 ctl captures
`command_id` → self-handoff → even if legacy marker disappears → automatic
`updated_healthy` → CP restores admin state → draining=false for previously active node
→ accepting/TLS/QUIC OK → heartbeat reports 1.1.14 → no manual reconciliation.
